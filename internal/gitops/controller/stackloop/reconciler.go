package stackloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Result describes the stack reconciliation outcome.
type Result struct {
	// SourceDigest is the digest of the source compose file before in-memory mutations.
	SourceDigest string
	// Services lists services defined in the reconciled compose file.
	Services []compose.Service
	// PrunedServices lists orphan services removed from the swarm stack.
	PrunedServices []string
	// Skipped reports whether deployment was skipped because the compose source digest was unchanged.
	Skipped bool
}

type stackDeployer interface {
	// DeployStack reconciles one stack via docker stack deploy command.
	DeployStack(ctx context.Context, stackName, composePath string, services []compose.Service) error
}

// Reconciler applies a desired stack state to swarm.
type Reconciler struct {
	cfg            *config.Config
	git            gitx.Repository
	deployer       stackDeployer
	pruner         *ServicePruner
	composeLoader  *compose.FileLoader
	composeRotator *Rotator
	pipeline       *pipeline
}

// New builds a stack reconciler loop.
func New(
	cfg *config.Config,
	gitSync gitx.Repository,
	stackDeployer *deployer.Deployer,
	swarmService *swarm.Swarm,
) *Reconciler {
	reconciler := &Reconciler{
		cfg:            cfg,
		git:            gitSync,
		deployer:       stackDeployer,
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
) (Result, error) {
	composePath := filepath.Join(r.git.WorkingDir(), req.Stack.ComposeFile)
	stackFile, err := r.composeLoader.Load(composePath)
	if err != nil {
		return Result{}, wrapReconcileError("load compose", nil, err)
	}

	result := Result{
		Services: stackFile.Compose.Services,
	}

	// Skip reconciliation when source compose content is unchanged since last successful apply.
	if req.HasPrev && req.PrevDigest == stackFile.Digest {
		result.SourceDigest = stackFile.Digest
		result.Skipped = true

		if req.IsManual {
			prunedServices, pruneErr := r.pruner.Prune(ctx, req.Stack, stackFile.Compose.Services)
			if pruneErr != nil {
				return result, wrapReconcileError("prune orphaned services", result.Services, pruneErr)
			}
			result.PrunedServices = prunedServices
		}

		return result, nil
	}

	deployComposePath := composePath
	composeChanged, pipeErr := r.pipeline.Run(stackFile, req.Stack.Name)
	if pipeErr != nil {
		return Result{}, wrapReconcileError(pipeErr.stepName, nil, pipeErr)
	}

	if composeChanged {
		renderedPath, renderedPathErr := r.writeRenderedCompose(req.Stack.Name, stackFile)
		if renderedPathErr != nil {
			return result, wrapReconcileError("write rendered compose", result.Services, renderedPathErr)
		}
		deployComposePath = renderedPath
	}

	// Deployer encapsulates init jobs orchestration and stack deployment.
	err = r.deployer.DeployStack(ctx, req.Stack.Name, deployComposePath, stackFile.Compose.Services)
	if err != nil {
		return result, wrapReconcileError("deploy", result.Services, err)
	}

	prunedServices, pruneErr := r.pruner.Prune(ctx, req.Stack, stackFile.Compose.Services)
	if pruneErr != nil {
		return result, wrapReconcileError("prune orphaned services", result.Services, pruneErr)
	}
	result.PrunedServices = prunedServices
	result.SourceDigest = stackFile.Digest
	return result, nil
}

func isManagedService(labels map[string]string) bool {
	return strings.TrimSpace(labels[labelsdict.ServiceManagedLabelKey]) == labelsdict.ServiceManagedLabelValue
}

func (r *Reconciler) writeRenderedCompose(stackName string, stackFile *compose.File) (string, error) {
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
