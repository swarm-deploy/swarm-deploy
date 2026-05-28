package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	managedServiceLabelKey         = "org.swarm-deploy.service.managed"
	managedServiceLabelValue       = "true"
	serviceSyncPolicyPruneLabelKey = "org.swarm-deploy.service.sync.policy.prune"
)

const serviceManagedLabel = "org.swarm-deploy.service.managed"

type stackReconcileResult struct {
	SourceDigest   string
	Services       []compose.Service
	PrunedServices []string
	Skipped        bool
}

type stackDeployer interface {
	// DeployStack reconciles one stack via docker stack deploy command.
	DeployStack(ctx context.Context, stackName, composePath string, services []compose.Service) error
}

type stackServiceManager interface {
	// ListStackServices returns services currently attached to a stack.
	ListStackServices(ctx context.Context, stackName string) ([]swarm.StackService, error)
	// Remove deletes service by docker identifier or full service name.
	Remove(ctx context.Context, serviceIDOrName string) error
}

type stackReconciler struct {
	cfg            *config.Config
	git            gitx.Repository
	deployer       stackDeployer
	pruner         *ServicePruner
	composeLoader  *compose.FileLoader
	composeRotator *Rotator
	pipeline       *pipeline
}

func newStackReconciler(
	cfg *config.Config,
	gitSync gitx.Repository,
	deployer *deployer.Deployer,
	swarmService *swarm.Swarm,
) *stackReconciler {
	reconciler := &stackReconciler{
		cfg:            cfg,
		git:            gitSync,
		deployer:       deployer,
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		pruner:         NewServicePruner(swarmService.Services, cfg.Spec.Sync.Policy),
	}

	reconciler.attachComposePipeline()

	return reconciler
}

func (r *stackReconciler) Reconcile(
	ctx context.Context,
	stackCfg config.StackSpec,
	prevDigest string,
	hasPrev bool,
	isManual bool,
) (stackReconcileResult, error) {
	composePath := filepath.Join(r.git.WorkingDir(), stackCfg.ComposeFile)
	stackFile, err := r.composeLoader.Load(composePath)
	if err != nil {
		return stackReconcileResult{}, wrapStackReconcileError("load compose", nil, err)
	}

	result := stackReconcileResult{
		Services: stackFile.Compose.Services,
	}

	// Skip reconciliation when source compose content is unchanged since last successful apply.
	if hasPrev && prevDigest == stackFile.Digest {
		result.SourceDigest = stackFile.Digest
		result.Skipped = true

		if isManual {
			prunedServices, pruneErr := r.pruner.Prune(ctx, stackCfg, stackFile.Compose.Services)
			if pruneErr != nil {
				return result, wrapStackReconcileError("prune orphaned services", result.Services, pruneErr)
			}
			result.PrunedServices = prunedServices
		}

		return result, nil
	}

	deployComposePath := composePath
	composeChanged, pipeErr := r.pipeline.Run(stackFile, stackCfg.Name)
	if pipeErr != nil {
		return stackReconcileResult{}, wrapStackReconcileError(pipeErr.stepName, nil, err)
	}

	if composeChanged {
		renderedPath, renderedPathErr := r.writeRenderedCompose(stackCfg.Name, stackFile)
		if renderedPathErr != nil {
			return result, wrapStackReconcileError("write rendered compose", result.Services, renderedPathErr)
		}
		deployComposePath = renderedPath
	}

	// Deployer encapsulates init jobs orchestration and stack deployment.
	err = r.deployer.DeployStack(ctx, stackCfg.Name, deployComposePath, stackFile.Compose.Services)
	if err != nil {
		return result, wrapStackReconcileError("deploy", result.Services, err)
	}

	prunedServices, pruneErr := r.pruner.Prune(ctx, stackCfg, stackFile.Compose.Services)
	if pruneErr != nil {
		return result, wrapStackReconcileError("prune orphaned services", result.Services, pruneErr)
	}
	result.PrunedServices = prunedServices
	result.SourceDigest = stackFile.Digest
	return result, nil
}

func isManagedService(labels map[string]string) bool {
	return strings.TrimSpace(labels[managedServiceLabelKey]) == managedServiceLabelValue
}

func (r *stackReconciler) writeRenderedCompose(stackName string, stackFile *compose.File) (string, error) {
	renderedDir := filepath.Join(r.cfg.Spec.DataDir, "rendered")
	// Persist rendered files under data dir so deploy step can use a stable path.
	if err := os.MkdirAll(renderedDir, 0o755); err != nil {
		return "", fmt.Errorf("create rendered dir: %w", err)
	}

	payload, err := stackFile.MarshalYAML()
	if err != nil {
		return "", err
	}

	target := filepath.Join(renderedDir, stackName+".yaml")
	err = os.WriteFile(target, payload, 0o600)
	if err != nil {
		return "", fmt.Errorf("write rendered compose %s: %w", target, err)
	}

	return target, nil
}
