package stackloop

import (
	"context"
	"log/slog"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type pipelinePayload struct {
	// Attempt is persisted before any external apply.
	Attempt          *deploymentmodel.Attempt
	Stack            config.StackSpec
	Commit           string
	IsNewDigest      bool
	IsManualSync     bool
	HasPreviousDrift bool

	Desired *compose.File

	LiveServices   []swarm.StackService
	ObservedAt     time.Time
	PrunedServices []string
	ExecutionError string
	RetryDeferred  bool
	// ProtectedServices cannot be remediated automatically while retry is deferred.
	ProtectedServices []string
	CleanupResult     rotatedCleanupResult
	Drift             map[string]drift.ServiceDrift
	RemainingDrift    map[string]drift.ServiceDrift
	Remediations      map[string]serviceRemediation
	RemediationErr    []error
}

func (r *Reconciler) attachPipeline() {
	r.pipeline = pipe.NewPipelineWithConfig[*pipelinePayload](pipe.Config{
		PipelineName: "sync stack",
		Logger:       faults.NewSafeLogger(slog.Default()),
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "execute deployment",
		Run:  r.executeDeployment,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "load live state",
		When: pipe.When(func(payload *pipelinePayload) bool { return payload.Attempt == nil }),
		Run:  r.loadLiveState,
	})

	r.pipeline.Add(pipe.Step[*pipelinePayload]{
		// Keep pruning deferred: the failed apply has not established a complete
		// desired stack, so removing prior services could interfere with recovery.
		Name: "prune orphaned services",
		When: pipe.When(func(payload *pipelinePayload) bool { return payload.Attempt == nil && !payload.RetryDeferred }),
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

func (r *Reconciler) executeDeployment(ctx context.Context, payload *pipelinePayload) error {
	result, err := r.executor.Execute(ctx, deployments.ExecuteRequest{
		Stack: payload.Stack, Commit: payload.Commit, Desired: *payload.Desired,
		SourceChanged: payload.IsNewDigest, ManualRetry: payload.IsManualSync,
	})
	payload.Attempt = result.Attempt
	payload.LiveServices = result.LiveServices
	payload.ObservedAt = result.ObservedAt
	payload.PrunedServices = result.PrunedServices
	payload.ExecutionError = result.ErrorCode
	payload.RetryDeferred = result.RetryDeferred
	payload.ProtectedServices = result.UnresolvedServices
	return err
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
