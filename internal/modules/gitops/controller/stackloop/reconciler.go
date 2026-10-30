package stackloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Reconciler applies a desired stack state to swarm.
type Reconciler struct {
	db               storage.Database
	deployments      *deployment.Service
	cfg              *config.Config
	git              gitx.Repository
	deployer         deployer.StackDeployer
	event            dispatcher.Dispatcher
	deployMetrics    metrics.Deploys
	stateStore       modelstore.Store
	fileSystem       fs.FileSystem
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
	reconcileMu      sync.Mutex
	pending          map[string]pendingCompletion
}

type pendingCompletion struct {
	attemptID  string
	state      model.Stack
	status     deploymentmodel.Status
	errorCode  string
	cause      error
	observedAt time.Time
}

const pruneFailureCode = "prune_failed"

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
	db storage.Database,
) *Reconciler {
	reconciler := &Reconciler{db: db, deployments: deployment.NewService(db, eventDispatcher),
		cfg:              cfg,
		git:              gitSync,
		deployer:         stackDeployer,
		event:            eventDispatcher,
		deployMetrics:    deployMetrics,
		stateStore:       stateStore,
		fileSystem:       fileSystem,
		composeLoader:    compose.NewFileLoaderWithReader(fileSystem.ReadFile),
		envFilePopulator: compose.NewEnvFilePopulator(),
		composeRotator:   NewRotator(fileSystem),
		pruner:           pruner.NewServicePruner(swarmService.Services, eventDispatcher, cfg.Spec.Sync.Policy),
		driftAnalyzer:    drift.NewAnalyzer(),
		serviceManager:   swarmService.Services,
		secretManager:    swarmService.Secrets,
		configManager:    swarmService.Configs,
		pending:          map[string]pendingCompletion{},
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

	if err := r.recoverPending(ctx, req.Stack.Name); err != nil {
		return fmt.Errorf("recover deployment result: %w", err)
	}

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
		HasPreviousDrift:  stackHasDrift(prev),
		Desired:           desiredState,
		DeployComposePath: desiredState.Path,
	}
	defer r.cleanupRenderedComposeFile(ctx, pl)

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
		LastDeployAt: prevState.LastDeployAt, Services: serviceStates,
	}
	if payload.Attempt != nil {
		result.LastDeployAt = now
	}
	err := r.persistResult(ctx, req, prevState, payload, result)
	if err != nil {
		if payload.Attempt != nil {
			r.pending[req.Stack.Name] = pendingCompletion{
				attemptID: payload.Attempt.ID, state: result, status: deploymentmodel.Succeeded,
				observedAt: payload.ObservedAt,
			}
		}
		return fmt.Errorf("persist stack result: %w", err)
	}
	if payload.Attempt != nil {
		for name := range serviceStates {
			r.deployMetrics.RecordDeploy(req.Stack.Name, name, "success")
		}
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
		failed := prev
		code := "apply_failed"
		status := deploymentmodel.Failed
		// Keep Docker job output out of returned errors and tracing; only the
		// bounded, masked excerpt is passed to durable failure diagnostics.
		var failureCause error = pipeErr
		switch pipeErr.StepName {
		case "deploy stack":
			var initJobFailure bool
			failureCause, initJobFailure = withInitJobLogs(pipeErr)
			if initJobFailure {
				code = "init_failed"
			}
		case "load live state":
			code, status = "verification_unknown", deploymentmodel.Interrupted
		case "prune orphaned services":
			code = pruneFailureCode
		}
		failed.LastCommit, failed.LastError = req.Commit, code
		if failed.Services == nil {
			failed.Services = map[string]model.Service{}
		}
		for _, service := range services {
			failed.Services[service.Name] = model.Service{
				Image: service.Image, SyncStatus: model.SyncStatusOutOfSync, SyncAt: time.Now(),
			}
		}
		failed.Status = model.NewStackStatus(failed.Services)
		switch code {
		case "verification_unknown":
			persistErr = r.deployments.InterruptVerificationWithError(ctx, pl.Attempt.ID, failed, failureCause)
		case pruneFailureCode:
			persistErr = r.deployments.FailCleanupWithError(ctx, pl.Attempt.ID, failed, code, pl.ObservedAt, failureCause)
		default:
			persistErr = r.deployments.FailApplyWithError(ctx, pl.Attempt.ID, failed, code, failureCause)
		}
		if persistErr != nil {
			r.pending[req.Stack.Name] = pendingCompletion{
				attemptID: pl.Attempt.ID, state: failed, status: status, errorCode: code,
				cause: faults.SafeError(failureCause), observedAt: pl.ObservedAt,
			}
		}
		for _, service := range services {
			r.deployMetrics.RecordDeploy(req.Stack.Name, service.Name, "failed")
		}
	}
	return wrapReconcileError(pipeErr.StepName, services, errors.Join(pipeErr, persistErr))
}

func (r *Reconciler) recoverPending(ctx context.Context, stack string) error {
	pending, ok := r.pending[stack]
	if !ok {
		return nil
	}
	var err error
	switch {
	case pending.status == deploymentmodel.Succeeded:
		err = r.deployments.SucceedObserved(ctx, pending.attemptID, pending.state, pending.observedAt)
	case pending.status == deploymentmodel.Interrupted:
		err = r.deployments.InterruptVerificationWithError(ctx, pending.attemptID, pending.state,
			pending.cause)
	case pending.errorCode == pruneFailureCode:
		err = r.deployments.FailCleanupWithError(ctx, pending.attemptID, pending.state, pending.errorCode,
			pending.observedAt, pending.cause)
	default:
		err = r.deployments.FailApplyWithError(ctx, pending.attemptID, pending.state, pending.errorCode,
			pending.cause)
	}
	if err == nil {
		delete(r.pending, stack)
		return nil
	}
	if !errors.Is(err, deploymentstore.ErrNotRunning) {
		return err
	}
	return r.resolveCompletionConflict(ctx, stack, pending, err)
}

func (r *Reconciler) resolveCompletionConflict(
	ctx context.Context, stack string, pending pendingCompletion, completionErr error,
) error {
	actual, err := r.deployments.Inspect(ctx, pending.attemptID)
	if err != nil {
		return fmt.Errorf("inspect deployment completion conflict: %w", err)
	}
	if actual.Stack != stack {
		return fmt.Errorf("deployment completion conflict stack mismatch: expected %s, actual %s",
			stack, actual.Stack)
	}
	switch actual.Status {
	case deploymentmodel.Succeeded, deploymentmodel.Failed, deploymentmodel.Interrupted, deploymentmodel.Superseded:
		slog.WarnContext(ctx, "deployment completion lifecycle conflict",
			slog.String("attempt_id", pending.attemptID),
			slog.String("stack", stack),
			slog.String("expected_status", string(pending.status)),
			slog.String("actual_status", string(actual.Status)),
		)
		delete(r.pending, stack)
		return nil
	case deploymentmodel.Running:
		return fmt.Errorf("completion returned not running but deployment %s is running: %w",
			pending.attemptID, completionErr)
	default:
		return fmt.Errorf("deployment %s has invalid status %q: %w",
			pending.attemptID, actual.Status, deployment.ErrInconsistentDeployment)
	}
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
		if payload.Attempt != nil {
			if err := r.deployments.SucceedObserved(ctx, payload.Attempt.ID, result, payload.ObservedAt); err != nil {
				return err
			}
		} else if err := r.stateStore.Update(ctx, func(state *model.Runtime) {
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
