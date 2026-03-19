package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/gitops"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

type stackReconcileResult struct {
	SourceDigest string
	Services     []compose.Service
	Skipped      bool
}

type stackReconciler struct {
	cfg      *config.Config
	gitSync  *gitops.Syncer
	deployer *swarm.Deployer
}

type stackReconcileError struct {
	op       string
	services []compose.Service
	err      error
}

func newStackReconciler(cfg *config.Config, gitSync *gitops.Syncer, deployer *swarm.Deployer) *stackReconciler {
	return &stackReconciler{
		cfg:      cfg,
		gitSync:  gitSync,
		deployer: deployer,
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
	composePath := filepath.Join(r.gitSync.WorkingDir(), stackCfg.ComposeFile)
	stackFile, err := compose.Load(composePath)
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

	if hasPrev && prevDigest == digest {
		result.SourceDigest = digest
		result.Skipped = true
		return result, nil
	}

	deployComposePath := composePath
	if r.cfg.Spec.SecretRotation.Enabled {
		if _, err := stackFile.ApplyObjectRotation(
			stackCfg.Name,
			composePath,
			r.cfg.Spec.SecretRotation.HashLength,
			r.cfg.Spec.SecretRotation.IncludePath,
		); err != nil {
			return result, wrapStackReconcileError("rotate objects", result.Services, err)
		}

		renderedPath, err := r.writeRenderedCompose(stackCfg.Name, stackFile)
		if err != nil {
			return result, wrapStackReconcileError("write rendered compose", result.Services, err)
		}
		deployComposePath = renderedPath
	}

	if err := r.runInitJobs(ctx, stackCfg.Name, stackFile.Services); err != nil {
		return result, wrapStackReconcileError("init jobs", result.Services, err)
	}

	if err := r.deployer.DeployStack(ctx, stackCfg.Name, deployComposePath); err != nil {
		return result, wrapStackReconcileError("deploy", result.Services, err)
	}

	result.SourceDigest = digest
	return result, nil
}

func (r *stackReconciler) runInitJobs(ctx context.Context, stackName string, services []compose.Service) error {
	for _, service := range services {
		for _, job := range service.InitJobs {
			err := r.deployer.RunInitJob(ctx, swarm.InitJobSpec{
				StackName:      stackName,
				ServiceName:    service.Name,
				DefaultNetwork: service.Networks,
				ServiceSecrets: service.Secrets,
				ServiceConfigs: service.Configs,
				Job:            job,
			})
			if err != nil {
				return fmt.Errorf("service %s init job %s: %w", service.Name, job.Name, err)
			}
		}
	}
	return nil
}

func (r *stackReconciler) writeRenderedCompose(stackName string, stackFile *compose.File) (string, error) {
	renderedDir := filepath.Join(r.cfg.Spec.DataDir, "rendered")
	if err := os.MkdirAll(renderedDir, 0o755); err != nil {
		return "", fmt.Errorf("create rendered dir: %w", err)
	}

	payload, err := stackFile.MarshalYAML()
	if err != nil {
		return "", err
	}

	target := filepath.Join(renderedDir, stackName+".yaml")
	if err := os.WriteFile(target, payload, 0o600); err != nil {
		return "", fmt.Errorf("write rendered compose %s: %w", target, err)
	}

	return target, nil
}
