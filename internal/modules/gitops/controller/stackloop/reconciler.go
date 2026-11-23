package stackloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
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
	pauseService    *syncpause.Service
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
	pauseServices ...*syncpause.Service,
) *Reconciler {
	reconciler := &Reconciler{db: db, executor: executor,
		cfg:            cfg,
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		preparer:       preparer,
		composeRotator: composeRotator,
		pruner:         pruner.NewServicePruner(swarmService.Services, eventDispatcher, cfg.Spec.GitOps.Sync.Policy),
		driftAnalyzer:  drift.NewAnalyzer(),
		serviceManager: swarmService.Services,
		secretManager:  swarmService.Secrets,
		configManager:  swarmService.Configs,
	}
	if len(pauseServices) > 0 {
		reconciler.pauseService = pauseServices[0]
	}
	reconciler.resourceCleaner = newRotatedResourceCleaner(
		reconciler.secretManager,
		reconciler.configManager,
		cfg.Spec.GitOps.ResourceRotation.Cleanup,
	)

	reconciler.attachPipeline()

	return reconciler
}

// Cleanup runs rotated config/secret cleanup for one stack without performing reconciliation.
func (r *Reconciler) Cleanup(ctx context.Context, stack config.StackSpec, commit string) error {
	desired, err := r.preparer.Load(ctx, stack, commit)
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
//
//nolint:gocognit // Reconciliation intentionally keeps preparation, pause policy and pipeline state together.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req ReconciliationRequest,
) error {
	desiredState, err := r.preparer.Load(ctx, req.Stack, req.Commit)
	if err != nil {
		persistErr := r.recordFailure(ctx, req.Stack.Name, req.Commit, nil, err)

		return wrapReconcileError("load compose", nil, errors.Join(err, persistErr))
	}

	prev, hasPrev, readErr := r.currentStackState(ctx, req.Stack.Name)
	if readErr != nil {
		return readErr
	}

	prepared, err := r.preparer.Prepare(ctx, preparation.Request{
		Stack: req.Stack, Desired: desiredState,
	})
	if err != nil {
		persistErr := r.recordFailure(ctx, req.Stack.Name, req.Commit, desiredState, err)
		stage := "prepare compose"
		if stepErr, ok := errors.AsType[*pipe.StepError](err); ok {
			stage = stepErr.StepName
		}
		return wrapReconcileError(stage, desiredState.Compose.Services, errors.Join(err, persistErr))
	}
	pl := &pipelinePayload{
		Stack:            req.Stack,
		Commit:           req.Commit,
		IsNewDigest:      !hasPrev || prev.SourceDigest != desiredState.Digest,
		IsManualSync:     req.IsManual,
		HasPreviousDrift: stackHasDrift(prev),
		Desired:          prepared,
		PendingSelfHeal:  map[string]bool{},
	}
	if r.pauseService != nil {
		policy, pauseErr := r.pauseService.Active(ctx, req.Stack.Name)
		if pauseErr != nil {
			return fmt.Errorf("read sync pause policy: %w", pauseErr)
		}
		pl.PreservedServices = syncpause.PreserveNames(policy, prepared.Compose.Services.Names(), prev)
	}
	// Persist prepared revisions before applying so an interruption cannot lose
	// the commit that introduced them.
	if err = r.recordPreparedDesired(ctx, req, prepared, &prev, pl.PreservedServices); err != nil {
		return err
	}
	pl.TargetServices, pl.ServiceCommits = targetServices(prepared, prev, pl.PreservedServices)
	for name, service := range prev.Services {
		if service.SelfHealPending {
			pl.PendingSelfHeal[name] = true
		}
	}

	err = r.pipeline.Run(ctx, pl)
	if err != nil {
		pipeErr, _ := errors.AsType[*pipe.StepError](err)
		if pl.RetryDeferred {
			return errors.Join(wrapReconcileError(pipeErr.StepName, nil, err), pl.RegistryAuthError)
		}

		return errors.Join(r.recordPipelineFailure(ctx, req, prev, pl, pipeErr), pl.RegistryAuthError)
	}

	if err = r.processResult(ctx, req, prev, desiredState, pl); err != nil {
		return errors.Join(err, pl.RegistryAuthError)
	}
	if r.cfg.Spec.GitOps.ResourceRotation.Cleanup.Enabled && (pl.Attempt != nil || pl.IsManualSync) {
		if err = r.cleanRotatedResources(ctx, pl); err != nil {
			slog.ErrorContext(ctx, "post-apply cleanup failed", "stack", req.Stack.Name, "err", err)
		}
	}
	return wrapReconcileError("refresh registry authentication", nil, pl.RegistryAuthError)
}

func stackHasDrift(stack model.Stack) bool {
	return len(stack.DriftedServices) > 0
}

func (r *Reconciler) currentStackState(ctx context.Context, stackName string) (model.Stack, bool, error) {
	currentState, err := r.stateStore.Read(ctx)
	if err != nil {
		return model.Stack{}, false, err
	}
	stack, ok := currentState.Stack(stackName)
	return stack, ok, nil
}

func (r *Reconciler) recordPreparedDesired(
	ctx context.Context,
	req ReconciliationRequest,
	prepared *compose.File,
	previous *model.Stack,
	preserved []string,
) error {
	desiredServices, err := r.persistDesiredServices(
		ctx, req.Stack.Name, req.Commit, prepared.ServiceDigests, preserved,
	)
	if err != nil {
		return wrapReconcileError("persist desired service state", prepared.Compose.Services, err)
	}
	// Failure handling must use the persisted desired scope so it cannot restore
	// applied revisions of removed services after a partial pruning failure.
	previous.Services = desiredServices
	return nil
}

func (r *Reconciler) persistDesiredServices(
	ctx context.Context,
	stackName string,
	commit string,
	digests map[string]string,
	preserved []string,
) (map[string]model.Service, error) {
	services := make(map[string]model.Service, len(digests))
	err := r.stateStore.Update(ctx, func(runtime *model.Runtime) {
		stack := runtime.Stacks[stackName]
		if stack.Services == nil {
			stack.Services = map[string]model.Service{}
		}
		for name, digest := range digests {
			service := stack.Services[name]
			if service.DesiredDigest != digest || service.DesiredCommit == "" {
				service.DesiredCommit = commit
			}
			service.DesiredDigest = digest
			service.ApplyPending = service.ApplyPending || service.AppliedDigest != digest
			stack.Services[name] = service
			services[name] = service
		}
		// A service leaves desired state before it can be pruned. Dropping its applied
		// revision in the same durable update means that restoring it deploys it again,
		// even when the process stops after pruning and before the result is recorded.
		removed := false
		for name, service := range stack.Services {
			if _, desired := digests[name]; desired {
				continue
			}
			if slices.Contains(preserved, name) {
				// Keep runtime visibility while the paused service is retained.
				// It must be eligible for deployment if restored in Git.
				service.DesiredDigest, service.DesiredCommit = "", ""
				service.AppliedDigest, service.AppliedCommit = "", ""
				service.ApplyPending = false
				stack.Services[name], services[name] = service, service
				continue
			}
			delete(stack.Services, name)
			removed = true
		}
		if removed {
			stack.Status = model.NewStackStatus(stack.Services)
		}
		runtime.Stacks[stackName] = stack
	})
	return services, err
}

//nolint:gocognit // Runtime projection preserves prior paused state across several independent outcome branches.
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
		previous := prevState.Services[service.Name]
		if slices.Contains(payload.PreservedServices, service.Name) {
			if previous.SyncStatus == model.SyncStatusUnspecified {
				previous.Image = service.Image
				previous.SyncStatus = model.SyncStatusOutOfSync
			}
			serviceStates[service.Name] = previous
			continue
		}
		state := serviceExecutionState(service, previous, payload, now)
		if state.SelfHealPending {
			state.SyncStatus = model.SyncStatusOutOfSync
			state.SyncError = prevState.Services[service.Name].SyncError
			if _, drifted := payload.RemainingDrift[service.Name]; !drifted {
				payload.RemainingDrift[service.Name] = drift.ServiceDrift{ServiceName: service.Name, Reason: state.SyncError}
			}
		}

		if serviceDrift, serviceDrifted := payload.RemainingDrift[service.Name]; serviceDrifted {
			state.SyncStatus = model.SyncStatusOutOfSync
			state.SyncError = serviceDrift.Reason
		}
		state = applySelfHealState(state, service.Name, payload)

		if payload.RetryDeferred && slices.Contains(payload.ProtectedServices, service.Name) {
			state = prevState.Services[service.Name]
		}
		serviceStates[service.Name] = state
	}
	for _, name := range payload.PreservedServices {
		if _, desired := serviceStates[name]; desired {
			continue
		}
		if previous, ok := prevState.Services[name]; ok {
			serviceStates[name] = previous
		}
	}

	driftedServices := prevState.DriftedServices
	if payload.RemainingDrift != nil {
		driftedServices = make(map[string]bool, len(payload.RemainingDrift))
		for name := range payload.RemainingDrift {
			driftedServices[name] = true
		}
		for _, name := range payload.PreservedServices {
			if prevState.DriftedServices[name] {
				driftedServices[name] = true
			}
		}
	}

	result := model.Stack{
		DriftedServices: driftedServices,
		SourceDigest:    desired.Digest, LastCommit: req.Commit, Status: model.NewStackStatus(serviceStates),
		LastDeployAt: prevState.LastDeployAt, StateUpdatedAt: now.UTC(), Services: serviceStates,
	}
	if payload.RetryDeferred {
		result.LastError = prevState.LastError
		result.LastCommit = prevState.LastCommit
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
	ctx context.Context, stackName, commit string, desired *compose.File, cause error,
) error {
	var services []compose.Service
	if desired != nil {
		services = desired.Compose.Services
	}
	safeCause := faults.SafeError(cause)
	return r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := r.stateStore.Update(ctx, func(runtime *model.Runtime) {
			state := runtime.Stacks[stackName]
			state.LastCommit, state.LastError = commit, events.DeployErrorCodePreparationFailed
			state.StateUpdatedAt = time.Now().UTC()
			if state.Services == nil {
				state.Services = map[string]model.Service{}
			}
			if desired == nil {
				for name, service := range state.Services {
					service.SyncStatus = model.SyncStatusOutOfSync
					state.Services[name] = service
				}
			}
			for _, service := range services {
				previous := state.Services[service.Name]
				state.Services[service.Name] = model.Service{
					Image: service.Image, DesiredDigest: previous.DesiredDigest, DesiredCommit: previous.DesiredCommit,
					AppliedDigest: previous.AppliedDigest, AppliedCommit: previous.AppliedCommit,
					ApplyPending: previous.ApplyPending,
					SyncStatus:   model.SyncStatusOutOfSync, SyncAt: time.Now(),
					SelfHealPending: previous.SelfHealPending, SyncError: previous.SyncError,
				}
			}
			state.Status = model.NewStackStatus(state.Services)
			runtime.Stacks[stackName] = state
		}); err != nil {
			return err
		}
		return r.event.Publish(ctx, &events.DeployPreparationFailed{
			StackName: stackName, Commit: commit, Services: services, ErrorCode: events.DeployErrorCodePreparationFailed,
			Stage: preparationFailureStage(cause), Error: safeCause,
		})
	})
}

func (r *Reconciler) recordPipelineFailure(
	ctx context.Context, req ReconciliationRequest, prev model.Stack, pl *pipelinePayload, pipeErr *pipe.StepError,
) error {
	services := pl.Desired.Compose.Services
	if pl.Attempt == nil {
		persistErr := r.recordFailure(ctx, req.Stack.Name, req.Commit, pl.Desired, pipeErr)
		return wrapReconcileError(pipeErr.StepName, services, errors.Join(pipeErr, persistErr))
	}
	// This write supersedes older completion events. Preserve receipts and the
	// stack's deployment time if a later reconciliation step failed.
	failed := prev
	failed.LastCommit = req.Commit
	failed.StateUpdatedAt = time.Now().UTC()
	if pl.ExecutionSucceeded {
		failed.LastError = postDeploymentFailureCode(pipeErr.StepName)
		failed.LastDeployAt = pl.Attempt.StartedAt
		projectAppliedRevisionsAfterFailure(&failed, pl)
	} else {
		failed.LastError = pl.ExecutionError
		if failed.LastError == "" {
			failed.LastError = events.DeployErrorCodeApplyFailed
		}
		for _, service := range pl.Attempt.Services(*pl.Desired) {
			state := failed.Services[service.Name]
			state.Image = service.Image
			state.SyncStatus, state.SyncAt = model.SyncStatusOutOfSync, failed.StateUpdatedAt
			state.SelfHealPending, state.SyncError = false, ""
			failed.Services[service.Name] = state
		}
	}
	failed.Status = model.NewStackStatus(failed.Services)
	persist := func(ctx context.Context) error {
		return r.stateStore.Update(ctx, func(runtime *model.Runtime) {
			runtime.Stacks[req.Stack.Name] = failed
		})
	}
	var persistErr error
	if pl.ExecutionSucceeded {
		persistErr = r.db.WithinTransaction(ctx, func(ctx context.Context) error {
			if err := persist(ctx); err != nil {
				return err
			}
			return r.event.Publish(ctx, &events.DeployPreparationFailed{
				StackName: req.Stack.Name, Commit: req.Commit, Services: services, ErrorCode: failed.LastError,
				Stage: pipeErr.StepName, Error: faults.SafeError(pipeErr),
			})
		})
	} else {
		persistErr = persist(ctx)
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
	// A nil map means drift analysis did not run. Deployment completion alone
	// cannot establish that an earlier drift episode was resolved.
	if payload.RemainingDrift == nil {
		return nil
	}
	for _, service := range payload.Desired.Compose.Services {
		if slices.Contains(payload.PreservedServices, service.Name) {
			continue
		}
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
	wasDrifted := prevState.DriftedServices[serviceName]
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
