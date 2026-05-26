package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
)

const serviceManagedLabel = "org.swarm-deploy.service.managed"

type stackReconcileResult struct {
	SourceDigest string
	Services     []compose.Service
	Skipped      bool
}

type stackReconciler struct {
	cfg            *config.Config
	git            gitx.Repository
	deployer       *deployer.Deployer
	composeLoader  *compose.FileLoader
	composeRotator *Rotator
	pipeline       *pipeline
}

func newStackReconciler(
	cfg *config.Config,
	gitSync gitx.Repository,
	deployer *deployer.Deployer,
) *stackReconciler {
	reconciler := &stackReconciler{
		cfg:            cfg,
		git:            gitSync,
		deployer:       deployer,
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
	}

	reconciler.attachComposePipeline()

	return reconciler
}

func (r *stackReconciler) Reconcile(
	ctx context.Context,
	stackCfg config.StackSpec,
	prevDigest string,
	hasPrev bool,
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

	result.SourceDigest = stackFile.Digest
	return result, nil
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
