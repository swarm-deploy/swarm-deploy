package stackloop

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type pipelinePayload struct {
	// Attempt is persisted before any external apply.
	Attempt      *deployment.Deployment
	Stack        config.StackSpec
	Commit       string
	IsNewDigest  bool
	IsManualSync bool

	Desired           *compose.File
	DesiredMutated    bool
	DeployComposePath string

	LiveServices   []swarm.StackService
	ObservedAt     time.Time
	PrunedServices []string
	CleanupResult  rotatedCleanupResult
	Drift          map[string]drift.ServiceDrift
}

func (r *Reconciler) attachPipeline() {
	r.pipeline = pipe.NewPipelineWithConfig[*pipelinePayload](pipe.Config{
		PipelineName: "sync stack",
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "populate environment",
		When: pipe.When(func(payload *pipelinePayload) bool {
			for _, service := range payload.Desired.Compose.Services {
				if len(service.EnvFiles) > 0 {
					return true
				}
			}

			return false
		}),
		Run: r.populateEnvironment,
	})

	if r.cfg.Spec.Containers.Downward != nil {
		r.pipeline.Add(pipe.Step[*pipelinePayload]{
			Name: "add downward",
			Run:  r.addDownward,
		})
	}

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "add managed label",
		Run:  r.addManagedLabel,
	})

	if r.cfg.Spec.SecretRotation.Enabled {
		r.pipeline.Add(pipe.Step[*pipelinePayload]{
			Name: "rotate secrets/configs",
			Run:  r.rotateSecrets,
		})
	}

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "write rendered compose",
		When: pipe.When(needsRenderedCompose),
		Run:  r.writeRenderedCompose,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "start deployment",
		Run: func(ctx context.Context, p *pipelinePayload) error {
			attempt, err := r.deployments.StartIfChanged(ctx, p.Stack.Name, p.Commit, *p.Desired, p.IsNewDigest)
			p.Attempt = attempt
			return err
		},
	})
	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "deploy stack",
		When: pipe.When(func(payload *pipelinePayload) bool {
			return payload.Attempt != nil
		}),
		Run: r.deployStack,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "load live state",
		Run:  r.loadLiveState,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "prune orphaned services",
		When: pipe.When(func(payload *pipelinePayload) bool {
			return payload.Attempt != nil || payload.IsManualSync
		}),
		Run: r.pruneOrphanedServices,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "analyze drift",
		When: pipe.When(func(payload *pipelinePayload) bool {
			return payload.Attempt == nil || payload.IsManualSync
		}),
		Run: r.analyzeDrift,
	})
}

func (r *Reconciler) populateEnvironment(_ context.Context, payload *pipelinePayload) error {
	changed := r.envFilePopulator.Populate(payload.Desired)

	if changed {
		payload.DesiredMutated = true
	}

	return nil
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

	rendered := stackDeployCompose(payload.Desired)
	r.normalizeRenderedObjectFilePaths(rendered)

	content, err := rendered.MarshalYAML()
	if err != nil {
		return fmt.Errorf("failed to marshal desired compose yaml: %w", err)
	}

	target := filepath.Join(renderedDir, payload.Stack.Name+".yaml")
	err = os.WriteFile(target, content, 0o600)
	if err != nil {
		return fmt.Errorf("write rendered compose %s: %w", target, err)
	}

	payload.DeployComposePath = target

	return nil
}

func (r *Reconciler) deployStack(ctx context.Context, payload *pipelinePayload) error {
	return r.deployer.DeployStack(
		ctx,
		payload.Stack.Name,
		payload.Desired.Path,
		payload.DeployComposePath,
		payload.Desired.Compose,
	)
}

func needsRenderedCompose(payload *pipelinePayload) bool {
	if payload.DesiredMutated {
		return true
	}

	for _, service := range payload.Desired.Compose.Services {
		if len(service.InitJobs) > 0 {
			return true
		}
	}

	return false
}

func stackDeployCompose(desired *compose.File) *compose.File {
	rendered := *desired
	rendered.Compose = desired.Compose
	rendered.Compose.Services = append(compose.Services(nil), desired.Compose.Services...)
	rendered.Compose.Configs = cloneObjects(desired.Compose.Configs)
	rendered.Compose.Secrets = cloneObjects(desired.Compose.Secrets)

	for i := range rendered.Compose.Services {
		rendered.Compose.Services[i].InitJobs = nil
	}

	return &rendered
}

func cloneObjects[T any, Objects ~map[string]*T](objects Objects) Objects {
	if objects == nil {
		return nil
	}

	cloned := make(Objects, len(objects))
	for alias, object := range objects {
		if object == nil {
			cloned[alias] = nil
			continue
		}

		objectCopy := *object
		cloned[alias] = &objectCopy
	}

	return cloned
}

func (r *Reconciler) normalizeRenderedObjectFilePaths(file *compose.File) {
	baseDir := filepath.Dir(file.Path)

	normalizeConfigFilePaths(baseDir, file.Compose.Configs)
	normalizeSecretFilePaths(baseDir, file.Compose.Secrets)

	repoDir := r.git.WorkingDir()

	for i, service := range file.Compose.Services {
		file.Compose.Services[i].EnvFiles = normalizeEnvFiles(repoDir, baseDir, service.EnvFiles)
	}
}

func normalizeEnvFiles(repoDir, baseDir string, envFiles []compose.EnvFile) []compose.EnvFile {
	result := make([]compose.EnvFile, len(envFiles))

	for i, file := range envFiles {
		result[i] = file
		if isRelativeFromRepoRoot(file.Path) {
			result[i].Path = filepath.Join(repoDir, file.Path)
		} else {
			result[i].Path = filepath.Join(baseDir, file.Path)
		}
	}

	return result
}

func isRelativeFromRepoRoot(path string) bool {
	return filepath.IsAbs(path)
}

func normalizeConfigFilePaths(baseDir string, objects compose.Configs) {
	for _, object := range objects {
		if object.External || object.File == "" || filepath.IsAbs(object.File) {
			continue
		}

		object.File = filepath.Clean(filepath.Join(baseDir, object.File))
	}
}

func normalizeSecretFilePaths(baseDir string, objects compose.Secrets) {
	for _, object := range objects {
		if object.External || object.File == "" || filepath.IsAbs(object.File) {
			continue
		}

		object.File = filepath.Clean(filepath.Join(baseDir, object.File))
	}
}

func (r *Reconciler) loadLiveState(ctx context.Context, payload *pipelinePayload) error {
	liveServices, err := r.serviceManager.ListStackServices(ctx, payload.Stack.Name)
	if err != nil {
		return err
	}

	payload.LiveServices = liveServices
	payload.ObservedAt = time.Now().UTC()
	return nil
}

func (r *Reconciler) pruneOrphanedServices(ctx context.Context, payload *pipelinePayload) error {
	prunedServices, err := r.pruner.Prune(ctx, pruner.PruneServicesRequest{
		Stack:   payload.Stack,
		Commit:  payload.Commit,
		Desired: payload.Desired.Compose.Services,
		Live:    payload.LiveServices,
	})
	if err != nil {
		return err
	}

	payload.PrunedServices = prunedServices

	return nil
}

func (r *Reconciler) cleanRotatedResources(ctx context.Context, payload *pipelinePayload) error {
	desiredConfigs, desiredSecrets, err := r.composeRotator.DesiredResourceNames(
		payload.Desired,
		payload.Stack.Name,
		r.cfg.Spec.SecretRotation.HashLength,
		r.cfg.Spec.SecretRotation.IncludePath,
	)
	if err != nil {
		slog.WarnContext(ctx, "[rotated-resource-cleaner] cleanup skipped: desired state is incomplete",
			slog.String("stack", payload.Stack.Name),
			slog.Any("error", err),
		)
		payload.CleanupResult.Skipped++
		return nil
	}

	var liveConfigs []swarm.Config
	loadedConfigs, configsErr := r.configManager.List(ctx, swarm.ListConfigsFilter{
		StackName: payload.Stack.Name,
		Labels: map[string]string{
			labelsdict.RotatedResourceManagedLabelKey: labelsdict.RotatedResourceManagedLabelValue,
		},
	})
	if configsErr != nil {
		slog.WarnContext(ctx, "[rotated-resource-cleaner] failed to load configs; config cleanup skipped",
			slog.String("stack", payload.Stack.Name),
			slog.Any("error", configsErr),
		)
		payload.CleanupResult.Skipped++
	} else {
		liveConfigs = loadedConfigs
	}

	var liveSecrets []swarm.Secret
	loadedSecrets, secretsErr := r.secretManager.List(ctx, swarm.ListSecretsFilter{
		StackName: payload.Stack.Name,
		Labels: map[string]string{
			labelsdict.RotatedResourceManagedLabelKey: labelsdict.RotatedResourceManagedLabelValue,
		},
	})
	if secretsErr != nil {
		slog.WarnContext(ctx, "[rotated-resource-cleaner] failed to load secrets; secret cleanup skipped",
			slog.String("stack", payload.Stack.Name),
			slog.Any("error", secretsErr),
		)
		payload.CleanupResult.Skipped++
	} else {
		liveSecrets = loadedSecrets
	}

	liveServices := liveServicesAfterPrune(payload.LiveServices, payload.PrunedServices)
	cleanerResult := r.resourceCleaner.clean(
		ctx,
		payload.Stack.Name,
		desiredConfigs,
		desiredSecrets,
		liveServices,
		liveConfigs,
		liveSecrets,
	)
	payload.CleanupResult.Removed += cleanerResult.Removed
	payload.CleanupResult.Failed += cleanerResult.Failed
	payload.CleanupResult.Skipped += cleanerResult.Skipped

	return nil
}

func liveServicesAfterPrune(services []swarm.StackService, pruned []string) []swarm.StackService {
	if len(pruned) == 0 {
		return services
	}

	prunedNames := make(map[string]struct{}, len(pruned))
	for _, name := range pruned {
		prunedNames[name] = struct{}{}
	}

	filtered := make([]swarm.StackService, 0, len(services))
	for _, service := range services {
		if _, wasPruned := prunedNames[service.Name]; wasPruned {
			continue
		}
		filtered = append(filtered, service)
	}

	return filtered
}

func (r *Reconciler) analyzeDrift(_ context.Context, payload *pipelinePayload) error {
	driftResp, err := r.driftAnalyzer.Analyze(drift.AnalyzeRequest{
		Stack:   payload.Stack,
		Desired: *payload.Desired,
		Live:    payload.LiveServices,
	})

	payload.Drift = driftResp.Drifts

	return err
}
