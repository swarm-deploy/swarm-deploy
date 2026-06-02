package stackloop

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
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
	pipeline       *pipeline
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
	}

	reconciler.attachComposePipeline()

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

		if req.IsManual {
			prunedServices, pruneErr := r.pruner.Prune(ctx, req.Stack, desiredState.Compose.Services)
			if pruneErr != nil {
				r.recordFailure(req.Stack.Name, req.Commit, result.Services, pruneErr)
				return result, wrapReconcileError("prune orphaned services", result.Services, pruneErr)
			}
			result.PrunedServices = prunedServices
		}

		return result, nil
	}

	_, pipeErr := r.pipeline.Run(&pipelinePayload{
		Stack:   req.Stack,
		Desired: desiredState,
	})
	if pipeErr != nil {
		r.recordFailure(req.Stack.Name, req.Commit, nil, pipeErr)
		return ReconciliationResponse{}, wrapReconcileError(pipeErr.stepName, nil, pipeErr)
	}

	// Deployer encapsulates init jobs orchestration and stack deployment.
	err = r.deployer.DeployStack(ctx, req.Stack.Name, desiredState.Path, desiredState.Compose.Services)
	if err != nil {
		r.recordFailure(req.Stack.Name, req.Commit, result.Services, err)
		return result, wrapReconcileError("deploy", result.Services, err)
	}

	prunedServices, pruneErr := r.pruner.Prune(ctx, req.Stack, desiredState.Compose.Services)
	if pruneErr != nil {
		r.recordFailure(req.Stack.Name, req.Commit, result.Services, pruneErr)
		return result, wrapReconcileError("prune orphaned services", result.Services, pruneErr)
	}
	result.PrunedServices = prunedServices
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

func isManagedService(labels map[string]string) bool {
	return strings.TrimSpace(labels[labelsdict.ServiceManagedLabelKey]) == labelsdict.ServiceManagedLabelValue
}
