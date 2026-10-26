package stackloop

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type serviceRemediation struct {
	attempted bool
	succeeded bool
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
			r.deployMetrics.RecordDeploy(payload.Stack.Name, service.Name, "failed")
			payload.RemediationErr = append(payload.RemediationErr,
				fmt.Errorf("restore service %s: %w", service.Name, err))
		} else {
			remediation.succeeded = true
			r.deployMetrics.RecordDeploy(payload.Stack.Name, service.Name, "success")
		}
		payload.Remediations[service.Name] = remediation
	}

	return nil
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
