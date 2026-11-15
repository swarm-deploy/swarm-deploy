package stackloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
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

	span := trace.SpanFromContext(ctx)

	for _, service := range payload.Desired.Compose.Services {
		if slices.Contains(payload.PreservedServices, service.Name) {
			continue
		}
		if payload.RetryDeferred && slices.Contains(payload.ProtectedServices, service.Name) {
			continue
		}
		_, drifted := payload.Drift[service.Name]
		if !drifted && !payload.PendingSelfHeal[service.Name] {
			span.AddEvent("Service skipped: no drift detected", trace.WithAttributes(
				tracing.ResourceServiceName.String(service.Name),
			))

			continue
		}

		if err := r.healService(ctx, payload, service, !drifted); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) healService(
	ctx context.Context,
	payload *pipelinePayload,
	service compose.Service,
	verifyOnly bool,
) error {
	ctx, span := selfHealTracer.Start(ctx, "stackReconciler.HealService", trace.WithAttributes(
		tracing.ResourceServiceName.String(service.Name),
	))
	defer span.End()

	policyName, enabled, err := resolveSelfHeal(
		service,
		payload.Stack.Sync.Policy.SelfHeal,
		r.cfg.Spec.GitOps.Sync.Policy.SelfHeal,
	)
	if err != nil {
		resolveErr := fmt.Errorf("resolve service %s self-heal policy: %w", service.Name, err)
		tracing.FailSpan(span, resolveErr)

		return resolveErr
	}
	if !enabled && !verifyOnly {
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
		VerifyOnly:        verifyOnly,
		StackName:         payload.Stack.Name,
		SourceComposePath: payload.Desired.Path,
		ServiceName:       service.Name,
		Desired:           payload.Desired.Compose,
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
			if remediation.applyErr != nil {
				payload.RemediationErr = append(payload.RemediationErr,
					fmt.Errorf("restore service %s: %w", name, remediation.applyErr))
				if errors.Is(remediation.applyErr, deployments.ErrConvergenceFailed) {
					remediation.failure = "Self Heal Convergence Failed"
				}
			}
			if _, drifted := payload.RemainingDrift[name]; !drifted {
				payload.RemainingDrift[name] = drift.ServiceDrift{ServiceName: name, Reason: remediation.failure}
			}
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
		remaining, drifted := verified[name]
		if !drifted && remediation.applyErr == nil {
			remediation.converged = true
			payload.Remediations[name] = remediation
			r.deployMetrics.RecordDeploy(payload.Stack.Name, name, "success")
			continue
		}
		remediation.failure = "Self Heal Execution Outcome Unknown"
		if drifted {
			remediation.failure = "Self Heal Verification Failed: " + remaining.Reason
		} else if errors.Is(remediation.applyErr, deployments.ErrConvergenceFailed) {
			remediation.failure = "Self Heal Convergence Failed"
		}
		if remediation.applyErr != nil {
			payload.RemediationErr = append(payload.RemediationErr,
				fmt.Errorf("restore service %s: %w", name, remediation.applyErr))
		} else {
			payload.RemediationErr = append(payload.RemediationErr,
				fmt.Errorf("verify service %s: %s", name, remaining.Reason))
		}
		if !drifted {
			// Configuration agreement cannot resolve an unsuccessful task recovery.
			// Retain the original drift episode until the deployment waiter confirms it.
			remaining = payload.Drift[name]
			if remaining.ServiceName == "" {
				remaining = drift.ServiceDrift{ServiceName: name, Reason: remediation.failure}
			}
			verified[name] = remaining
		}
		r.deployMetrics.RecordDeploy(payload.Stack.Name, name, "failed")
		payload.Remediations[name] = remediation
	}
}

func applySelfHealState(state model.Service, name string, payload *pipelinePayload) model.Service {
	remediation, remediated := payload.Remediations[name]
	if !remediated || !remediation.attempted {
		return state
	}
	state.SelfHealPending = !remediation.converged
	if remediation.converged {
		state.SyncStatus = model.SyncStatusSynced
		state.SyncError = ""
		delete(payload.RemainingDrift, name)
	} else {
		state.SyncStatus = model.SyncStatusOutOfSync
		state.SyncError = remediation.failure
	}
	return state
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
