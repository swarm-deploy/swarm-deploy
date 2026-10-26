package stackloop

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

type serviceRemediation struct {
	attempted bool
	applyErr  error
	converged bool
	failure   string
}

func (r *Reconciler) selfHealDrift(ctx context.Context, payload *pipelinePayload) error {
	payload.Remediations = make(map[string]serviceRemediation)
	var deployCompose compose.Compose
	deployComposeReady := false

	for _, service := range payload.Desired.Compose.Services {
		if _, drifted := payload.Drift[service.Name]; !drifted {
			continue
		}

		enabled, err := resolveSelfHeal(service, r.cfg.Spec.Sync.Policy.SelfHeal)
		if err != nil {
			return fmt.Errorf("resolve service %s self-heal policy: %w", service.Name, err)
		}
		if !enabled {
			continue
		}
		if !deployComposeReady {
			deployFile := stackDeployCompose(payload.Desired, payload.Stack.Name)
			r.normalizeRenderedObjectFilePaths(deployFile)
			deployCompose = deployFile.Compose
			deployComposeReady = true
		}

		remediation := serviceRemediation{attempted: true}
		_, err = r.deployer.DeployService(
			ctx, payload.Stack.Name, payload.Desired.Path, service.Name,
			payload.Desired.Compose, deployCompose,
		)
		if err != nil {
			remediation.applyErr = err
			slog.ErrorContext(ctx, "self-heal apply command failed; awaiting state verification",
				"stack", payload.Stack.Name, "service", service.Name, "err", faults.SafeError(err))
		}
		payload.Remediations[service.Name] = remediation
	}

	return nil
}

func (r *Reconciler) verifySelfHeal(ctx context.Context, payload *pipelinePayload) error {
	live, err := r.serviceManager.ListStackServices(ctx, payload.Stack.Name)
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

func resolveSelfHeal(service compose.Service, global bool) (bool, error) {
	policy, specified, err := service.SelfHealPolicy()
	if err != nil {
		return false, err
	}
	if specified {
		return policy, nil
	}

	return global, nil
}
