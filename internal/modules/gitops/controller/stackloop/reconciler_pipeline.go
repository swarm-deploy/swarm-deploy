package stackloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	renderedComposeDirectoryMode = 0o755
	renderedComposeFileMode      = 0o600
	initJobPersistenceTimeout    = 2 * time.Second
)

type pipelinePayload struct {
	// Attempt is persisted before any external apply.
	Attempt          *deploymentmodel.Attempt
	Stack            config.StackSpec
	Commit           string
	IsNewDigest      bool
	IsManualSync     bool
	HasPreviousDrift bool

	Desired             *compose.File
	DesiredMutated      bool
	DeployComposePath   string
	RenderedComposePath string

	LiveServices   []swarm.StackService
	ObservedAt     time.Time
	PrunedServices []string
	CleanupResult  rotatedCleanupResult
	Drift          map[string]drift.ServiceDrift
	RemainingDrift map[string]drift.ServiceDrift
	Remediations   map[string]serviceRemediation
	RemediationErr []error
}

func (r *Reconciler) attachPipeline() {
	r.pipeline = pipe.NewPipelineWithConfig[*pipelinePayload](pipe.Config{
		PipelineName: "sync stack",
		Logger:       faults.NewSafeLogger(slog.Default()),
	})

	if r.cfg.Spec.Sync.Policy.Image.Enabled() {
		r.pipeline.Add(pipe.Step[*pipelinePayload]{
			Name: "check image policy",
			Run:  r.checkImagePolicy,
		})
	}

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
		Run:  r.startDeployment,
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
		Run:  r.pruneOrphanedServices,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "analyze drift",
		When: pipe.When(func(payload *pipelinePayload) bool {
			return payload.Attempt == nil || payload.IsManualSync || payload.HasPreviousDrift
		}),
		Run: r.analyzeDrift,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "self-heal drift",
		When: pipe.When(func(payload *pipelinePayload) bool {
			return payload.Attempt == nil && len(payload.Drift) > 0
		}),
		Run: r.selfHealDrift,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "verify self-heal",
		When: pipe.When(func(payload *pipelinePayload) bool {
			return hasRemediationAttempt(payload.Remediations)
		}),
		Run: r.verifySelfHeal,
	})
}

func (r *Reconciler) startDeployment(ctx context.Context, payload *pipelinePayload) error {
	attempt, err := r.creation.StartIfChanged(
		ctx, payload.Stack.Name, payload.Commit, *payload.Desired, payload.IsNewDigest,
		r.deployments.ProjectInterruption,
	)
	payload.Attempt = attempt
	return err
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

	for i, service := range payload.Desired.Compose.Services {
		serviceChanged := service.Deploy.Labels.Add(labelsdict.ServiceManagedLabelKey, labelsdict.ServiceManagedLabelValue)

		if serviceChanged {
			payload.Desired.Compose.Services[i] = service
			changed = true
		}
	}
	if changed {
		payload.DesiredMutated = true
	}

	return nil
}

func (r *Reconciler) rotateSecrets(ctx context.Context, payload *pipelinePayload) error {
	// Rotation mutates secret/config object names in the in-memory compose model.
	// We keep digest based on original source, but deploy a rendered, rotated file.
	changed, err := r.composeRotator.Rotate(
		ctx, payload.Desired,
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

func (r *Reconciler) writeRenderedCompose(ctx context.Context, payload *pipelinePayload) error {
	renderedDir := filepath.Join(r.cfg.Spec.DataDir, "rendered")
	// Persist rendered files under data dir so deploy step can use a stable path.
	if err := r.fileSystem.CreateDirectory(ctx, renderedDir, renderedComposeDirectoryMode); err != nil {
		return fmt.Errorf("create rendered dir: %w", err)
	}

	rendered := stackDeployCompose(payload.Desired, payload.Stack.Name)
	r.normalizeRenderedObjectFilePaths(rendered)

	content, err := rendered.MarshalYAML()
	if err != nil {
		return fmt.Errorf("failed to marshal desired compose yaml: %w", err)
	}

	target := filepath.Join(renderedDir, payload.Stack.Name+".yaml")
	payload.RenderedComposePath = target
	err = r.fileSystem.WriteFile(ctx, target, content, renderedComposeFileMode)
	if err != nil {
		return fmt.Errorf("write rendered compose %s: %w", target, err)
	}

	payload.DeployComposePath = target

	return nil
}

func (r *Reconciler) cleanupRenderedComposeFile(ctx context.Context, payload *pipelinePayload) {
	if payload.RenderedComposePath == "" {
		return
	}
	if err := r.fileSystem.RemoveFile(ctx, payload.RenderedComposePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.WarnContext(ctx, "failed to clean up rendered compose file", "path", payload.RenderedComposePath, "err", err)
	}
}

func (r *Reconciler) deployStack(ctx context.Context, payload *pipelinePayload) error {
	result, deployErr := r.deployer.DeployStack(
		ctx,
		payload.Stack.Name,
		payload.Desired.Path,
		payload.DeployComposePath,
		payload.Desired.Compose,
	)
	r.persistInitJobs(ctx, payload.Attempt.ID, result.InitJobs)
	return deployErr
}

func (r *Reconciler) persistInitJobs(ctx context.Context, attemptID string, results []deployer.InitJobResult) {
	for index, result := range results {
		r.persistInitJobExecution(ctx, deploymentmodel.InitJobExecution{
			AttemptID:     attemptID,
			Ordinal:       index + 1,
			ServiceName:   result.ServiceName,
			JobName:       result.JobName,
			Status:        deploymentmodel.InitJobStatus(result.Status),
			StartedAt:     result.StartedAt,
			FinishedAt:    result.FinishedAt,
			Logs:          result.Logs,
			LogsAvailable: result.LogsAvailable,
			LogsTruncated: result.LogsTruncated,
		})
	}
}

func (r *Reconciler) persistInitJobExecution(
	ctx context.Context,
	execution deploymentmodel.InitJobExecution,
) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), initJobPersistenceTimeout)
	defer cancel()
	if err := r.deployments.SaveInitJobExecution(persistCtx, execution); err != nil {
		slog.WarnContext(
			persistCtx,
			"persist init job execution",
			slog.String("attempt_id", execution.AttemptID),
			slog.String("service_name", execution.ServiceName),
			slog.String("job_name", execution.JobName),
			slog.Any("err", err),
		)
	}
}

func needsRenderedCompose(payload *pipelinePayload) bool {
	if payload.DesiredMutated {
		return true
	}
	for _, secret := range payload.Desired.Compose.Secrets {
		if !secret.External {
			return true
		}
	}

	for _, service := range payload.Desired.Compose.Services {
		if len(service.InitJobs) > 0 {
			return true
		}
	}

	return false
}

func stackDeployCompose(desired *compose.File, stackName string) *compose.File {
	rendered := *desired
	rendered.Compose = desired.Compose
	rendered.Compose.Services = append(compose.Services(nil), desired.Compose.Services...)
	rendered.Compose.Configs = cloneObjects(desired.Compose.Configs)
	rendered.Compose.Secrets = cloneObjects(desired.Compose.Secrets)

	// The API reconciler owns secret creation. Docker CLI receives references
	// for all API-managed secrets, independently of their source format.
	for alias, secret := range rendered.Compose.Secrets {
		if secret.External {
			continue
		}
		name := secret.Name
		if name == "" {
			name = stackName + "_" + alias
		}
		rendered.Compose.Secrets[alias] = &compose.Secret{Name: name, External: true}
	}

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

func normalizeEnvFiles(repoDir, baseDir string, envFiles compose.EnvFiles) compose.EnvFiles {
	result := make(compose.EnvFiles, len(envFiles))

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
	liveServices, err := r.serviceManager.ListServices(ctx, swarm.ListServicesFilter{Stack: payload.Stack.Name})
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
		ctx, payload.Desired,
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
	payload.RemainingDrift = driftResp.Drifts

	return err
}
