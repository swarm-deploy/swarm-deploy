package stackloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func (r *Reconciler) attachComposePipeline() {
	pipe := newPipeline()

	pipe.Add(pipelineStep{
		name: "add managed label",
		run:  r.addManagedLabel,
	})

	if r.cfg.Spec.SecretRotation.Enabled {
		pipe.Add(pipelineStep{
			name: "rotate secrets/configs",
			run:  r.rotateSecrets,
		})
	}

	pipe.Add(pipelineStep{
		name: "write rendered compose",
		when: func(payload *pipelinePayload) bool {
			return payload.DesiredMutated
		},
		run: r.writeRenderedCompose,
	})

	pipe.Add(pipelineStep{
		name: "deploy stack",
		run:  r.deployStack,
	})

	r.pipeline = pipe
}

func (r *Reconciler) addManagedLabel(_ context.Context, payload *pipelinePayload) error {
	changed := false

	for _, service := range payload.Desired.Compose.Services {
		present := service.Deploy.Labels.Add(labelsdict.ServiceManagedLabelKey, labelsdict.ServiceManagedLabelValue)
		if !present {
			changed = true
		}
	}

	if changed {
		payload.DesiredMutated = true
	}

	return nil
}

func (r *Reconciler) rotateSecrets(_ context.Context, payload *pipelinePayload) error {
	// Rotation mutates secret/config object names in the in-memory compose model.
	// We keep digest based on original source, but deploy a rendered, rotated file.
	changed, err := r.composeRotator.Rotate(
		payload.Desired,
		payload.Stack.Name,
		r.cfg.Spec.SecretRotation.HashLength,
		r.cfg.Spec.SecretRotation.IncludePath,
	)
	if err != nil {
		return err
	}

	if changed {
		payload.DesiredMutated = true
	}

	return nil
}

func (r *Reconciler) writeRenderedCompose(_ context.Context, payload *pipelinePayload) error {
	renderedDir := filepath.Join(r.cfg.Spec.DataDir, "rendered")
	// Persist rendered files under data dir so deploy step can use a stable path.
	if err := os.MkdirAll(renderedDir, 0o755); err != nil {
		return fmt.Errorf("create rendered dir: %w", err)
	}

	content, err := payload.Desired.MarshalYAML()
	if err != nil {
		return fmt.Errorf("failed to marshal desired compose yaml: %w", err)
	}

	target := filepath.Join(renderedDir, payload.Stack.Name+".yaml")
	err = os.WriteFile(target, content, 0o600)
	if err != nil {
		return fmt.Errorf("write rendered compose %s: %w", target, err)
	}

	payload.Desired.Path = target

	return nil
}

func (r *Reconciler) deployStack(ctx context.Context, payload *pipelinePayload) error {
	return r.deployer.DeployStack(ctx, payload.Stack.Name, payload.Desired.Path, payload.Desired.Compose.Services)
}
