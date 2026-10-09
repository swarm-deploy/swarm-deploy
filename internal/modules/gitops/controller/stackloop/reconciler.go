package stackloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Reconciler applies a desired stack state to swarm.
type Reconciler struct {
	db               *storage.Database
	deployments      *deployment.Service
	cfg              *config.Config
	git              gitx.Repository
	deployer         deployer.StackDeployer
	event            dispatcher.Dispatcher
	deployMetrics    metrics.Deploys
	stateStore       modelstore.Store
	pruner           *pruner.ServicePruner
	composeLoader    compose.FileLoader
	envFilePopulator *compose.EnvFilePopulator
	composeRotator   *Rotator
	pipeline         *pipe.Pipeline[*pipelinePayload]
	driftAnalyzer    *drift.Analyzer
	serviceManager   swarm.ServiceManager
	secretManager    swarm.SecretManager
	configManager    swarm.ConfigManager
	resourceCleaner  *rotatedResourceCleaner
}

// New builds a stack reconciler loop.
func New(
	cfg *config.Config,
	gitSync gitx.Repository,
	stackDeployer deployer.StackDeployer,
	swarmService *swarm.Swarm,
	eventDispatcher dispatcher.Dispatcher,
	deployMetrics metrics.Deploys,
	stateStore modelstore.Store,
	fileSystem fs.FileSystem,
	db *storage.Database,
) *Reconciler {
	reconciler := &Reconciler{db: db, deployments: deployment.NewService(db, eventDispatcher),
		cfg:              cfg,
		git:              gitSync,
		deployer:         stackDeployer,
		event:            eventDispatcher,
		deployMetrics:    deployMetrics,
		stateStore:       stateStore,
		composeLoader:    compose.NewFileLoaderWithReader(fileSystem.ReadFile),
		envFilePopulator: compose.NewEnvFilePopulator(),
		composeRotator:   NewRotator(),
		pruner:           pruner.NewServicePruner(swarmService.Services, eventDispatcher, cfg.Spec.Sync.Policy),
		driftAnalyzer:    drift.NewAnalyzer(),
		serviceManager:   swarmService.Services,
		secretManager:    swarmService.Secrets,
		configManager:    swarmService.Configs,
	}
	reconciler.resourceCleaner = newRotatedResourceCleaner(
		reconciler.secretManager,
		reconciler.configManager,
		cfg.Spec.SecretRotation.Cleanup,
	)

	reconciler.attachPipeline()

	return reconciler
}

// Cleanup runs rotated config/secret cleanup for one stack without performing reconciliation.
func (r *Reconciler) Cleanup(ctx context.Context, stack config.StackSpec) error {
	composePath := filepath.Join(r.git.WorkingDir(), stack.ComposeFile)
	desired, err := r.composeLoader.Load(ctx, composePath)
	if err != nil {
		return fmt.Errorf("load compose for cleanup: %w", err)
	}

	liveServices, err := r.serviceManager.ListStackServices(ctx, stack.Name)
	if err != nil {
		return fmt.Errorf("list stack services for cleanup: %w", err)
	}

	payload := &pipelinePayload{
		Stack:        stack,
		Desired:      desired,
		LiveServices: liveServices,
	}
	err = r.cleanRotatedResources(ctx, payload)
	if err != nil {
		return err
	}
	if payload.CleanupResult.Failed > 0 || payload.CleanupResult.Skipped > 0 {
		return fmt.Errorf(
			"rotated resource cleanup incomplete: removed=%d failed=%d skipped=%d",
			payload.CleanupResult.Removed,
			payload.CleanupResult.Failed,
			payload.CleanupResult.Skipped,
		)
	}

	return nil
}

// Reconcile applies one stack definition.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req ReconciliationRequest,
) error {
	composePath := filepath.Join(r.git.WorkingDir(), req.Stack.ComposeFile)
	desiredState, err := r.composeLoader.Load(ctx, composePath)
	if err != nil {
		persistErr := r.recordFailure(ctx, req.Stack.Name, req.Commit, nil, err)

		return wrapReconcileError("load compose", nil, errors.Join(err, persistErr))
	}

	prev, hasPrev, readErr := r.currentStackState(ctx, req.Stack.Name)
	if readErr != nil {
		return readErr
	}

	pl := &pipelinePayload{
		Stack:             req.Stack,
		Commit:            req.Commit,
		IsNewDigest:       !hasPrev || prev.SourceDigest != desiredState.Digest,
		IsManualSync:      req.IsManual,
		Desired:           desiredState,
		DeployComposePath: desiredState.Path,
	}

	err = r.pipeline.Run(ctx, pl)
	if err != nil {
		pipeErr, _ := errors.AsType[*pipe.StepError](err)

		return r.recordPipelineFailure(ctx, req, prev, pl, pipeErr)

	}

	if err = r.processResult(ctx, req, prev, desiredState, pl); err != nil {
		return err
	}
	if r.cfg.Spec.SecretRotation.Cleanup.Enabled && (pl.Attempt != nil || pl.IsManualSync) {
		if err = r.cleanRotatedResources(ctx, pl); err != nil {
			slog.ErrorContext(ctx, "post-apply cleanup failed", "stack", req.Stack.Name, "err", err)
		}
	}
	return nil
}

func (r *Reconciler) currentStackState(ctx context.Context, stackName string) (model.Stack, bool, error) {
	currentState, err := r.stateStore.Read(ctx)
	if err != nil {
		return model.Stack{}, false, err
	}
	stack, ok := currentState.Stack(stackName)
	return stack, ok, nil
}

func (r *Reconciler) processResult(
	ctx context.Context,
	req ReconciliationRequest,
	prevState model.Stack,
	desired *compose.File,
	payload *pipelinePayload,
) error {
	now := time.Now()

	serviceStates := make(map[string]model.Service, len(desired.Compose.Services))
	for _, service := range desired.Compose.Services {
		state := model.Service{
			Image:      service.Image,
			SyncStatus: model.SyncStatusSynced,
			SyncAt:     now,
		}

		if serviceDrift, serviceDrifted := payload.Drift[service.Name]; serviceDrifted {
			state.SyncStatus = model.SyncStatusOutOfSync
			state.SyncError = serviceDrift.Reason
		}

		serviceStates[service.Name] = state
	}

	result := model.Stack{
		SourceDigest: desired.Digest, LastCommit: req.Commit, Status: model.NewStackStatus(serviceStates),
		LastDeployAt: prevState.LastDeployAt, Services: serviceStates,
	}
	if payload.Attempt != nil {
		result.LastDeployAt = now
	}
	err := r.persistResult(ctx, req, prevState, payload, result)
	if err != nil {
		return fmt.Errorf("persist stack result: %w", err)
	}
	if payload.Attempt != nil {
		for name := range serviceStates {
			r.deployMetrics.RecordDeploy(req.Stack.Name, name, "success")
		}
	}
	return nil
}

func (r *Reconciler) recordFailure(
	ctx context.Context, stackName, commit string, services []compose.Service, _ error,
) error {
	return r.stateStore.Update(ctx, func(runtime *model.Runtime) {
		state := runtime.Stacks[stackName]
		state.LastCommit, state.LastError = commit, "preparation_failed"
		if state.Services == nil {
			state.Services = map[string]model.Service{}
		}
		for _, service := range services {
			state.Services[service.Name] = model.Service{
				Image: service.Image, SyncStatus: model.SyncStatusOutOfSync, SyncAt: time.Now(),
			}
		}
		state.Status = model.NewStackStatus(state.Services)
		runtime.Stacks[stackName] = state
	})
}

func (r *Reconciler) recordPipelineFailure(
	ctx context.Context, req ReconciliationRequest, prev model.Stack, pl *pipelinePayload, pipeErr *pipe.StepError,
) error {
	services := pl.Desired.Compose.Services
	var persistErr error
	if pl.Attempt == nil {
		persistErr = r.recordFailure(ctx, req.Stack.Name, req.Commit, services, pipeErr)
	} else {
		failed := prev
		failed.LastCommit, failed.LastError = req.Commit, "apply_failed"
		if failed.Services == nil {
			failed.Services = map[string]model.Service{}
		}
		for _, service := range services {
			failed.Services[service.Name] = model.Service{
				Image: service.Image, SyncStatus: model.SyncStatusOutOfSync, SyncAt: time.Now(),
			}
		}
		failed.Status = model.NewStackStatus(failed.Services)
		code := "apply_failed"
		if pipeErr.StepName == "prune orphaned services" {
			code = "prune_failed"
		}
		persistErr = r.deployments.Fail(ctx, pl.Attempt.ID, failed, code)
		for _, service := range services {
			r.deployMetrics.RecordDeploy(req.Stack.Name, service.Name, "failed")
		}
	}
	return wrapReconcileError(pipeErr.StepName, services, errors.Join(pipeErr, persistErr))
}

func (r *Reconciler) persistResult(
	ctx context.Context, req ReconciliationRequest, prevState model.Stack, payload *pipelinePayload, result model.Stack,
) error {
	return r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if payload.Attempt != nil {
			if err := r.deployments.Succeed(ctx, payload.Attempt.ID, result); err != nil {
				return err
			}
		} else if err := r.stateStore.Update(ctx, func(state *model.Runtime) {
			state.Stacks[req.Stack.Name] = result
		}); err != nil {
			return err
		}
		for _, d := range payload.Drift {
			if !d.ServiceMissed || prevState.ServiceSyncStatus(d.ServiceName) == model.SyncStatusOutOfSync {
				continue
			}
			event := &events.ServiceMissed{StackName: req.Stack.Name, ServiceName: d.ServiceName, Commit: req.Commit}
			if err := r.event.Publish(ctx, event); err != nil {
				return err
			}
		}
		return nil
	})
}
