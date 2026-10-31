package stackloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Reconciler applies a desired stack state to swarm.
type Reconciler struct {
	db              storage.Database
	executor        deployments.Executor
	cfg             *config.Config
	event           dispatcher.Dispatcher
	deployMetrics   metrics.Deploys
	stateStore      modelstore.Store
	pruner          *pruner.ServicePruner
	preparer        *preparation.Preparer
	composeRotator  *Rotator
	pipeline        *pipe.Pipeline[*pipelinePayload]
	driftAnalyzer   *drift.Analyzer
	serviceManager  swarm.ServiceManager
	secretManager   swarm.SecretManager
	configManager   swarm.ConfigManager
	resourceCleaner *rotatedResourceCleaner
	reconcileMu     sync.Mutex
}

// New builds a stack reconciler loop.
func New(
	cfg *config.Config,
	executor deployments.Executor,
	swarmService *swarm.Swarm,
	eventDispatcher dispatcher.Dispatcher,
	deployMetrics metrics.Deploys,
	stateStore modelstore.Store,
	preparer *preparation.Preparer,
	composeRotator *Rotator,
	db storage.Database,
) *Reconciler {
	reconciler := &Reconciler{db: db, executor: executor,
		cfg:            cfg,
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		preparer:       preparer,
		composeRotator: composeRotator,
		pruner:         pruner.NewServicePruner(swarmService.Services, eventDispatcher, cfg.Spec.Sync.Policy),
		driftAnalyzer:  drift.NewAnalyzer(),
		serviceManager: swarmService.Services,
		secretManager:  swarmService.Secrets,
		configManager:  swarmService.Configs,
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
	desired, err := r.preparer.Load(ctx, stack)
	if err != nil {
		return fmt.Errorf("load compose for cleanup: %w", err)
	}

	liveServices, err := r.serviceManager.ListServices(ctx, swarm.ListServicesFilter{Stack: stack.Name})
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
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()

	desiredState, err := r.preparer.Load(ctx, req.Stack)
	if err != nil {
		persistErr := r.recordFailure(ctx, req.Stack.Name, req.Commit, nil, err)

		return wrapReconcileError("load compose", nil, errors.Join(err, persistErr))
	}

	prev, hasPrev, readErr := r.currentStackState(ctx, req.Stack.Name)
	if readErr != nil {
		return readErr
	}

	prepared, err := r.preparer.Prepare(ctx, preparation.Request{
		Stack: req.Stack, Desired: desiredState, SourceChanged: !hasPrev || prev.SourceDigest != desiredState.Digest,
	})
	if err != nil {
		persistErr := r.recordFailure(ctx, req.Stack.Name, req.Commit, desiredState.Compose.Services, err)
		stage := "prepare compose"
		if stepErr, ok := errors.AsType[*pipe.StepError](err); ok {
			stage = stepErr.StepName
		}
		return wrapReconcileError(stage, desiredState.Compose.Services, errors.Join(err, persistErr))
	}
	defer r.preparer.Cleanup(ctx, prepared)

	pl := &pipelinePayload{
		Stack:             req.Stack,
		Commit:            req.Commit,
		IsNewDigest:       !hasPrev || prev.SourceDigest != desiredState.Digest,
		IsManualSync:      req.IsManual,
		HasPreviousDrift:  stackHasDrift(prev),
		Desired:           prepared.Desired,
		DeployComposePath: prepared.DeployComposePath,
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

func stackHasDrift(stack model.Stack) bool {
	for _, service := range stack.Services {
		if service.SyncStatus == model.SyncStatusOutOfSync {
			return true
		}
	}
	return false
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

		if serviceDrift, serviceDrifted := payload.RemainingDrift[service.Name]; serviceDrifted {
			state.SyncStatus = model.SyncStatusOutOfSync
			state.SyncError = serviceDrift.Reason
		}
		if remediation, remediated := payload.Remediations[service.Name]; remediated && remediation.attempted {
			if remediation.converged {
				state.SyncStatus = model.SyncStatusSynced
				state.SyncError = ""
			} else {
				state.SyncStatus = model.SyncStatusOutOfSync
				state.SyncError = remediation.failure
			}
		}

		serviceStates[service.Name] = state
	}

	result := model.Stack{
		SourceDigest: desired.Digest, LastCommit: req.Commit, Status: model.NewStackStatus(serviceStates),
		LastDeployAt: prevState.LastDeployAt, StateUpdatedAt: now.UTC(), Services: serviceStates,
	}
	if payload.Attempt != nil {
		result.LastDeployAt = now
	}
	err := r.persistResult(ctx, req, prevState, payload, result)
	if err != nil {
		return fmt.Errorf("persist stack result: %w", err)
	}
	if len(payload.RemediationErr) > 0 {
		return fmt.Errorf("self-heal drift: %w", errors.Join(payload.RemediationErr...))
	}
	return nil
}

func hasRemediationAttempt(remediations map[string]serviceRemediation) bool {
	for _, remediation := range remediations {
		if remediation.attempted {
			return true
		}
	}

	return false
}

func (r *Reconciler) recordFailure(
	ctx context.Context, stackName, commit string, services []compose.Service, cause error,
) error {
	safeCause := faults.SafeError(cause)
	return r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := r.stateStore.Update(ctx, func(runtime *model.Runtime) {
			state := runtime.Stacks[stackName]
			state.LastCommit, state.LastError = commit, "preparation_failed"
			state.StateUpdatedAt = time.Now().UTC()
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
		}); err != nil {
			return err
		}
		return r.event.Publish(ctx, &events.DeployPreparationFailed{
			StackName: stackName, Commit: commit, Services: services, ErrorCode: "preparation_failed",
			Stage: preparationFailureStage(cause), Error: safeCause,
		})
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
		// Deployments has already classified and committed the terminal Attempt.
		// GitOps keeps the reconciliation error observable and lets the durable
		// completion event update its runtime projection.
		failed := prev
		failed.LastCommit, failed.LastError = req.Commit, pl.ExecutionError
		failed.StateUpdatedAt = time.Now().UTC()
		if failed.LastError == "" {
			failed.LastError = "apply_failed"
		}
		if failed.Services == nil {
			failed.Services = map[string]model.Service{}
		}
		for _, service := range services {
			failed.Services[service.Name] = model.Service{
				Image: service.Image, SyncStatus: model.SyncStatusOutOfSync, SyncAt: time.Now(),
			}
		}
		failed.Status = model.NewStackStatus(failed.Services)
		persistErr = r.stateStore.Update(ctx, func(runtime *model.Runtime) {
			runtime.Stacks[req.Stack.Name] = failed
		})
	}
	return wrapReconcileError(pipeErr.StepName, services, errors.Join(pipeErr, persistErr))
}

func preparationFailureStage(err error) string {
	var readErr *compose.ReadComposeError
	if errors.As(err, &readErr) {
		return "source fetching"
	}
	var parseErr *compose.ParseComposeError
	var validationErr *compose.ValidateComposeError
	if errors.As(err, &parseErr) || errors.As(err, &validationErr) {
		return "validation"
	}
	return "preparation"
}

func (r *Reconciler) persistResult(
	ctx context.Context, req ReconciliationRequest, prevState model.Stack, payload *pipelinePayload, result model.Stack,
) error {
	return r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := r.stateStore.Update(ctx, func(state *model.Runtime) {
			state.Stacks[req.Stack.Name] = result
		}); err != nil {
			return err
		}
		return r.publishDriftEvents(ctx, req, prevState, payload)
	})
}

func (r *Reconciler) publishDriftEvents(
	ctx context.Context,
	req ReconciliationRequest,
	prevState model.Stack,
	payload *pipelinePayload,
) error {
	for _, service := range payload.Desired.Compose.Services {
		if err := r.publishServiceDriftEvents(ctx, req, prevState, payload, service.Name); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) publishServiceDriftEvents(
	ctx context.Context,
	req ReconciliationRequest,
	prevState model.Stack,
	payload *pipelinePayload,
	serviceName string,
) error {
	initial, initiallyDrifted := payload.Drift[serviceName]
	final, unresolved := payload.RemainingDrift[serviceName]
	previousStatus := prevState.ServiceSyncStatus(serviceName)
	wasDrifted := previousStatus == model.SyncStatusOutOfSync
	detected := final
	if !unresolved {
		detected = initial
	}
	if (initiallyDrifted || unresolved) && !wasDrifted {
		if detected.ServiceMissed {
			if err := r.event.Publish(ctx, &events.ServiceMissed{
				StackName: req.Stack.Name, ServiceName: serviceName, Commit: req.Commit, Resolved: !unresolved,
			}); err != nil {
				return err
			}
		} else if err := r.event.Publish(ctx, &events.ServiceDrifted{
			StackName: req.Stack.Name, ServiceName: serviceName, Commit: req.Commit,
			Reason: detected.Reason, Resolved: !unresolved,
		}); err != nil {
			return err
		}
	}
	if !unresolved && (wasDrifted || initiallyDrifted) {
		return r.event.Publish(ctx, &events.ServiceDriftFixed{
			StackName: req.Stack.Name, ServiceName: serviceName, Commit: req.Commit,
		})
	}
	return nil
}
