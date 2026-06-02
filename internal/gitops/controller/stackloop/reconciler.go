package stackloop

import (
	"context"
	"path/filepath"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller/stackloop/drift"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/pipe"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Reconciler applies a desired stack state to swarm.
type Reconciler struct {
	cfg            *config.Config
	git            gitx.Repository
	deployer       deployer.StackDeployer
	stateStore     modelstore.Store
	pruner         *ServicePruner
	composeLoader  *compose.FileLoader
	composeRotator *Rotator
	pipeline       *pipe.Pipeline[*pipelinePayload]
	driftAnalyzer  *drift.Analyzer
}

// New builds a stack reconciler loop.
func New(
	cfg *config.Config,
	gitSync gitx.Repository,
	stackDeployer deployer.StackDeployer,
	swarmService *swarm.Swarm,
	stateStore modelstore.Store,
) *Reconciler {
	reconciler := &Reconciler{
		cfg:            cfg,
		git:            gitSync,
		deployer:       stackDeployer,
		stateStore:     stateStore,
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		pruner:         NewServicePruner(swarmService.Services, cfg.Spec.Sync.Policy),
		driftAnalyzer:  drift.NewAnalyzer(),
	}

	reconciler.attachPipeline()

	return reconciler
}

// Reconcile applies one stack definition.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req ReconciliationRequest,
) (ReconciliationResponse, error) {
	composePath := filepath.Join(r.git.WorkingDir(), req.Stack.ComposeFile)
	desiredState, err := r.composeLoader.Load(composePath)
	if err != nil {
		r.recordFailure(req.Stack.Name, req.Commit, nil, err)
		return ReconciliationResponse{}, wrapReconcileError("load compose", nil, err)
	}

	result := ReconciliationResponse{
		Services: desiredState.Compose.Services,
	}
	prev, hasPrev := r.currentStackState(req.Stack.Name)

	// Skip reconciliation when source compose content is unchanged since last successful apply.
	if hasPrev && prev.SourceDigest == desiredState.Digest {
		result.SourceDigest = desiredState.Digest
		result.Skipped = true
	}

	pl := &pipelinePayload{
		Stack:        req.Stack,
		IsNewDigest:  !hasPrev || prev.SourceDigest != desiredState.Digest,
		IsManualSync: req.IsManual,
		Desired:      desiredState,
	}

	pipeErr := r.pipeline.Run(ctx, pl)
	if pipeErr != nil {
		r.recordFailure(req.Stack.Name, req.Commit, result.Services, pipeErr)
		return ReconciliationResponse{}, wrapReconcileError(pipeErr.StepName, nil, pipeErr)
	}

	result.SourceDigest = desiredState.Digest

	r.recordSuccess(req.Stack.Name, req.Commit, result.SourceDigest, result.Services)
	return result, nil
}

func (r *Reconciler) currentStackState(stackName string) (model.Stack, bool) {
	currentState := r.stateStore.Get()
	stackState, exists := currentState.Stacks[stackName]
	return stackState, exists
}

func (r *Reconciler) recordSuccess(
	stackName string,
	commit string,
	sourceDigest string,
	services []compose.Service,
) {
	now := time.Now()
	servicesState := make(map[string]model.Service, len(services))
	for _, service := range services {
		servicesState[service.Name] = model.Service{
			Image:      service.Image,
			SyncStatus: model.SyncStatusSynced,
			SyncAt:     now,
		}
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
