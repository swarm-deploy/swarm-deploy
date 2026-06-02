package stackloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/pipe"
)

type pipelinePayload struct {
	Stack        config.StackSpec
	IsNewDigest  bool
	IsManualSync bool

	Desired        *compose.File
	DesiredMutated bool

	PrunedServices []string
}

func (r *Reconciler) attachPipeline() {
	pipeline := pipe.NewPipeline[*pipelinePayload]()

	pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "add managed label",
		When: func(payload *pipelinePayload) bool {
			return payload.IsNewDigest
		},
		Run: r.addManagedLabel,
	})

	if r.cfg.Spec.SecretRotation.Enabled {
		pipeline.Add(pipe.Step[*pipelinePayload]{
			Name: "rotate secrets/configs",
			When: func(payload *pipelinePayload) bool {
				return payload.IsNewDigest
			},
			Run: r.rotateSecrets,
		})
	}

	pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "write rendered compose",
		When: func(payload *pipelinePayload) bool {
			return payload.DesiredMutated
		},
		Run: r.writeRenderedCompose,
	})

	pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "deploy stack",
		When: func(payload *pipelinePayload) bool {
			return payload.IsNewDigest || payload.DesiredMutated
		},
		Run: r.deployStack,
	})

	pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "prune orphaned services",
		When: func(payload *pipelinePayload) bool {
			return payload.IsNewDigest || payload.IsManualSync
		},
		Run: r.pruneOrphanedServices,
	})

	r.pipeline = pipeline
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

func (r *Reconciler) pruneOrphanedServices(ctx context.Context, payload *pipelinePayload) error {
	prunedServices, err := r.pruner.Prune(ctx, payload.Stack, payload.Desired.Compose.Services)
	if err != nil {
		return err
	}

	payload.PrunedServices = prunedServices

	return nil
}
