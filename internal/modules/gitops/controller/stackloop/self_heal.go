package stackloop

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var selfHealTracer = otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop")

type serviceRemediation struct {
	attempted bool
	applyErr  error
	converged bool
	failure   string
}

func (r *Reconciler) selfHealDrift(ctx context.Context, payload *pipelinePayload) error {
	payload.Remediations = make(map[string]serviceRemediation)
	deployCompose := r.preparer.DeploymentCompose(payload.Desired, payload.Stack.Name)

	span := trace.SpanFromContext(ctx)

	for _, service := range payload.Desired.Compose.Services {
		if _, drifted := payload.Drift[service.Name]; !drifted {
			span.AddEvent("Service skipped: no drift detected", trace.WithAttributes(
				tracing.ResourceServiceName.String(service.Name),
			))

			continue
		}

		if err := r.healService(ctx, payload, service, deployCompose); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) healService(
	ctx context.Context,
	payload *pipelinePayload,
	service compose.Service,
	deployCompose compose.Compose,
) error {
	ctx, span := selfHealTracer.Start(ctx, "stackReconciler.HealService", trace.WithAttributes(
		tracing.ResourceServiceName.String(service.Name),
	))
	defer span.End()

	policyName, enabled, err := resolveSelfHeal(
		service,
		payload.Stack.Sync.Policy.SelfHeal,
		r.cfg.Spec.Sync.Policy.SelfHeal,
	)
	if err != nil {
		resolveErr := fmt.Errorf("resolve service %s self-heal policy: %w", service.Name, err)
		tracing.FailSpan(span, resolveErr)

		return resolveErr
	}
	if !enabled {
		span.AddEvent("Service skipped by sync policy", trace.WithAttributes(
			tracing.ResourceSelfHealPolicyName(policyName),
			tracing.ResourceSelfHealPolicyEnabled(enabled),
		))

		return nil
	}

	span.SetAttributes(
		tracing.ResourceSelfHealPolicyName(policyName),
		tracing.ResourceSelfHealPolicyEnabled(enabled),
	)

	remediation := serviceRemediation{attempted: true}
	_, err = r.executor.ExecuteService(ctx, deployments.ExecuteServiceRequest{
		StackName: payload.Stack.Name, SourceComposePath: payload.Desired.Path,
		ServiceName: service.Name, Desired: payload.Desired.Compose, Deploy: deployCompose,
	})
	if err != nil {
		remediation.applyErr = err
		slog.ErrorContext(ctx, "self-heal apply command failed; awaiting state verification",
			"stack", payload.Stack.Name, "service", service.Name, "err", faults.SafeError(err))
		tracing.FailSpan(span, err)
	}
	payload.Remediations[service.Name] = remediation

	return nil
}

func (r *Reconciler) verifySelfHeal(ctx context.Context, payload *pipelinePayload) error {
	live, err := r.serviceManager.ListServices(ctx, swarm.ListServicesFilter{Stack: payload.Stack.Name})
	if err != nil {
		r.markVerificationFailed(payload, fmt.Errorf("verify self-heal state: %w", err))
		return nil
	}

	verified, err := r.driftAnalyzer.Analyze(drift.AnalyzeRequest{
		Stack: payload.Stack, Desired: *payload.Desired, Live: live,
	})
	if err != nil {
		r.markVerificationFailed(payload, fmt.Errorf("analyze self-heal result: %w", err))
		return nil
	}

	payload.LiveServices = live
	payload.ObservedAt = time.Now().UTC()
	payload.RemainingDrift = verified.Drifts
	r.applyVerifiedDrift(payload, verified.Drifts)

	return nil
}

func (r *Reconciler) markVerificationFailed(payload *pipelinePayload, err error) {
	payload.RemediationErr = append(payload.RemediationErr, err)
	for name, remediation := range payload.Remediations {
		if remediation.attempted {
			remediation.failure = "Self Heal Verification Failed"
			payload.Remediations[name] = remediation
			r.deployMetrics.RecordDeploy(payload.Stack.Name, name, "failed")
		}
	}
}

func (r *Reconciler) applyVerifiedDrift(
	payload *pipelinePayload, verified map[string]drift.ServiceDrift,
) {
	for name, remediation := range payload.Remediations {
		if !remediation.attempted {
			continue
		}
		if remaining, drifted := verified[name]; drifted {
			remediation.failure = "Self Heal Verification Failed: " + remaining.Reason
			if remediation.applyErr != nil {
				payload.RemediationErr = append(payload.RemediationErr,
					fmt.Errorf("restore service %s: %w", name, remediation.applyErr))
			} else {
				payload.RemediationErr = append(payload.RemediationErr,
					fmt.Errorf("verify service %s: %s", name, remaining.Reason))
			}
			r.deployMetrics.RecordDeploy(payload.Stack.Name, name, "failed")
		} else {
			remediation.converged = true
			r.deployMetrics.RecordDeploy(payload.Stack.Name, name, "success")
		}
		payload.Remediations[name] = remediation
	}
}

func resolveSelfHeal(service compose.Service, stack *bool, global bool) (string, bool, error) {
	policy, specified, err := service.SelfHealPolicy()
	if err != nil {
		return "service", false, err
	}
	if specified {
		return "service", policy, nil
	}
	if stack != nil {
		return "stack", *stack, nil
	}

	return "global", global, nil
}
