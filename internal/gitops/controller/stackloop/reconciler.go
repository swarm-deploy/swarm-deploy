package stackloop

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller/stackloop/pruner"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/pipe"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Reconciler applies a desired stack state to swarm.
type Reconciler struct {
	cfg            *config.Config
	git            gitx.Repository
	deployer       deployer.StackDeployer
	event          dispatcher.Dispatcher
	deployMetrics  metrics.Deploys
	stateStore     modelstore.Store
	pruner         *pruner.ServicePruner
	composeLoader  *compose.FileLoader
	composeRotator *Rotator
	pipeline       *pipe.Pipeline[*pipelinePayload]
	driftAnalyzer  *drift.Analyzer
	serviceManager swarm.ServiceManager
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
) *Reconciler {
	reconciler := &Reconciler{
		cfg:            cfg,
		git:            gitSync,
		deployer:       stackDeployer,
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		pruner:         pruner.NewServicePruner(swarmService.Services, eventDispatcher, cfg.Spec.Sync.Policy),
		driftAnalyzer:  drift.NewAnalyzer(),
		serviceManager: swarmService.Services,
	}

	reconciler.attachPipeline()

	return reconciler
}

// Reconcile applies one stack definition.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req ReconciliationRequest,
) error {
	composePath := filepath.Join(r.git.WorkingDir(), req.Stack.ComposeFile)
	desiredState, err := r.composeLoader.Load(composePath)
	if err != nil {
		r.recordFailure(req.Stack.Name, req.Commit, nil, err)
		r.recordStackFailure(req.Stack.Name, req.Commit, nil, err)
		return wrapReconcileError("load compose", nil, err)
	}

	services := desiredState.Compose.Services
	prev, hasPrev := r.currentStackState(req.Stack.Name)
	skipped := hasPrev && prev.SourceDigest == desiredState.Digest

	pl := &pipelinePayload{
		Stack:        req.Stack,
		Commit:       req.Commit,
		IsNewDigest:  !hasPrev || prev.SourceDigest != desiredState.Digest,
		IsManualSync: req.IsManual,
		Desired:      desiredState,
	}

	pipeErr := r.pipeline.Run(ctx, pl)
	if pipeErr != nil {
		r.recordFailure(req.Stack.Name, req.Commit, services, pipeErr)
		r.recordStackFailure(req.Stack.Name, req.Commit, services, pipeErr)
		return wrapReconcileError(pipeErr.StepName, services, pipeErr)
	}

	r.recordSuccess(ctx, req.Stack.Name, req.Commit, services, skipped)
	r.recordState(req.Stack.Name, req.Commit, desiredState.Digest, pl, services)
	return nil
}

func (r *Reconciler) currentStackState(stackName string) (model.Stack, bool) {
	currentState := r.stateStore.Get()
	stackState, exists := currentState.Stacks[stackName]
	return stackState, exists
}

func (r *Reconciler) recordState(
	stackName string,
	commit string,
	sourceDigest string,
	payload *pipelinePayload,
	services []compose.Service,
) {
	now := time.Now()
	servicesState := make(map[string]model.Service, len(services))
	for _, service := range services {
		state := model.Service{
			Image:      service.Image,
			SyncStatus: model.SyncStatusSynced,
			SyncAt:     now,
		}

		if serviceDrift, serviceDrifted := payload.Drift[service.Name]; serviceDrifted {
			state.SyncStatus = model.SyncStatusOutOfSync
			state.SyncError = serviceDrift.Reason
		}

		servicesState[service.Name] = state
	}

	r.stateStore.Update(func(state *model.Runtime) {
		state.Stacks[stackName] = model.Stack{
			SourceDigest: sourceDigest,
			LastCommit:   commit,
			Status:       model.NewStackStatus(servicesState),
			LastError:    "",
			LastDeployAt: now,
			Services:     servicesState,
		}
	})
}

func (r *Reconciler) recordFailure(
	stackName string,
	commit string,
	services []compose.Service,
	reason error,
) {
	now := time.Now()
	servicesState := make(map[string]model.Service, len(services))
	for _, service := range services {
		servicesState[service.Name] = model.Service{
			Image:      service.Image,
			SyncStatus: model.SyncStatusOutOfSync,
			SyncAt:     now,
		}
	}

	r.stateStore.Update(func(state *model.Runtime) {
		state.Stacks[stackName] = model.Stack{
			SourceDigest: "",
			LastCommit:   commit,
			Status:       model.NewStackStatus(servicesState),
			LastError:    reason.Error(),
			LastDeployAt: now,
			Services:     servicesState,
		}
	})
}

func (r *Reconciler) recordSuccess(
	ctx context.Context,
	stackName string,
	commit string,
	services []compose.Service,
	skipped bool,
) {
	if !skipped {
		for _, service := range services {
			r.deployMetrics.RecordDeploy(stackName, service.Name, "success")
		}

		r.event.Dispatch(ctx, &events.DeploySuccess{
			StackName: stackName,
			Commit:    commit,
			Services:  services,
		})
	}
}

func (r *Reconciler) recordStackFailure(
	stackName string,
	commit string,
	services []compose.Service,
	reason error,
) {
	for _, service := range services {
		r.deployMetrics.RecordDeploy(stackName, service.Name, "failed")
	}
	if len(services) == 0 {
		r.deployMetrics.RecordDeploy(stackName, "unknown", "failed")
	}

	logs := []string{}

	var logsErr containsLogsError
	if errors.As(reason, &logsErr) {
		logs = logsErr.Logs()
	}

	r.event.Dispatch(context.Background(), &events.DeployFailed{
		StackName: stackName,
		Commit:    commit,
		Services:  services,
		Error:     reason,
		Logs:      logs,
	})
}

type containsLogsError interface {
	Logs() []string
}
