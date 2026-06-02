package stackloop

import (
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func (r *Reconciler) attachComposePipeline() {
	composePipelineSteps := []pipelineStep{
		{
			name: "add managed label",
			run:  r.addManagedLabel,
		},
	}

	if r.cfg.Spec.SecretRotation.Enabled {
		composePipelineSteps = append(composePipelineSteps, pipelineStep{
			name: "rotate secrets/configs",
			run:  r.rotateSecrets,
		})
	}

	r.pipeline = newPipeline(composePipelineSteps)
}

func (r *Reconciler) addManagedLabel(payload *pipelinePayload) (bool, error) {
	changed := false

	for _, service := range payload.Desired.Compose.Services {
		present := service.Deploy.Labels.Add(labelsdict.ServiceManagedLabelKey, labelsdict.ServiceManagedLabelValue)
		if !present {
			changed = true
		}
	}

	return changed, nil
}

func (r *Reconciler) rotateSecrets(payload *pipelinePayload) (bool, error) {
	// Rotation mutates secret/config object names in the in-memory compose model.
	// We keep digest based on original source, but deploy a rendered, rotated file.
	return r.composeRotator.Rotate(
		payload.Desired,
		payload.Stack.Name,
		r.cfg.Spec.SecretRotation.HashLength,
		r.cfg.Spec.SecretRotation.IncludePath,
	)
}
