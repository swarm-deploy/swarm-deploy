package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
)

type stackReconcileResult struct {
	SourceDigest string
	Services     []compose.Service
	Skipped      bool
}

type stackReconciler struct {
	cfg           *config.Config
	git           gitx.Repository
	deployer      *deployer.Deployer
	composeLoader *compose.Loader
}

type stackReconcileError struct {
	op       string
	services []compose.Service
	err      error
}

func newStackReconciler(
	cfg *config.Config,
	gitSync gitx.Repository,
	deployer *deployer.Deployer,
) *stackReconciler {
	return &stackReconciler{
		cfg:           cfg,
		git:           gitSync,
		deployer:      deployer,
		composeLoader: compose.NewLoader(),
	}
}

func (e *stackReconcileError) Error() string {
	return fmt.Sprintf("%s: %v", e.op, e.err)
}

func (e *stackReconcileError) Unwrap() error {
	return e.err
}

func (e *stackReconcileError) FailedServices() []compose.Service {
	return e.services
}

func wrapStackReconcileError(op string, services []compose.Service, err error) error {
	if err == nil {
		return nil
	}

	return &stackReconcileError{
		op:       op,
		services: services,
		err:      err,
	}
}

func failedServicesFromReconcileError(err error) []compose.Service {
	var reconcileErr *stackReconcileError
	// Preserve detailed service context when the caller receives wrapped errors.
	if errors.As(err, &reconcileErr) {
		return reconcileErr.FailedServices()
	}
	return nil
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
		Services: stackFile.Services,
	}

	digest, err := stackFile.ComputeDigest(composePath)
	if err != nil {
		return result, wrapStackReconcileError("compute digest", result.Services, err)
	}

	// Skip reconciliation when source compose content is unchanged since last successful apply.
	if hasPrev && prevDigest == digest {
		result.SourceDigest = digest
		result.Skipped = true
		return result, nil
	}

	deployComposePath := composePath
	if r.cfg.Spec.SecretRotation.Enabled {
		// Rotation mutates secret/config object names in the in-memory compose model.
		// We keep digest based on original source, but deploy a rendered, rotated file.
		_, err = stackFile.ApplyObjectRotation(
			stackCfg.Name,
			composePath,
			r.cfg.Spec.SecretRotation.HashLength,
			r.cfg.Spec.SecretRotation.IncludePath,
		)
		if err != nil {
			return result, wrapStackReconcileError("rotate objects", result.Services, err)
		}

		renderedPath, renderedPathErr := r.writeRenderedCompose(stackCfg.Name, stackFile)
		if renderedPathErr != nil {
			return result, wrapStackReconcileError("write rendered compose", result.Services, renderedPathErr)
		}
		deployComposePath = renderedPath
	}

	// Deployer encapsulates init jobs orchestration and stack deployment.
	err = r.deployer.DeployStack(ctx, stackCfg.Name, deployComposePath, stackFile.Services)
	if err != nil {
		return result, wrapStackReconcileError("deploy", result.Services, err)
	}

	result.SourceDigest = digest
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
