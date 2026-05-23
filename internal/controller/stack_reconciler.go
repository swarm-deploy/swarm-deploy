package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	managedServiceLabelKey         = "org.swarm-deploy.service.managed"
	managedServiceLabelValue       = "true"
	serviceSyncPolicyPruneLabelKey = "org.swarm-deploy.service.sync.policy.prune"
)

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
	cfg      *config.Config
	git      gitx.Repository
	deployer stackDeployer
	services stackServiceManager
}

type stackReconcileError struct {
	op       string
	services []compose.Service
	err      error
}

func newStackReconciler(
	cfg *config.Config,
	gitSync gitx.Repository,
	deployer stackDeployer,
	services stackServiceManager,
) *stackReconciler {
	return &stackReconciler{
		cfg:      cfg,
		git:      gitSync,
		deployer: deployer,
		services: services,
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

	// Skip reconciliation when source compose content is unchanged since last successful apply.
	if hasPrev && prevDigest == digest {
		result.SourceDigest = digest
		result.Skipped = true

		prunedServices, pruneErr := r.pruneOrphanedServices(ctx, stackCfg, stackFile.Services)
		if pruneErr != nil {
			return result, wrapStackReconcileError("prune orphaned services", result.Services, pruneErr)
		}
		result.PrunedServices = prunedServices

		return result, nil
	}

	deployComposePath := composePath
	renderCompose := false
	managedLabelsChanged, err := stackFile.ApplyServiceDeployLabels(map[string]string{
		managedServiceLabelKey: managedServiceLabelValue,
	})
	if err != nil {
		return result, wrapStackReconcileError("apply managed service labels", result.Services, err)
	}
	if managedLabelsChanged {
		renderCompose = true
	}

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
		renderCompose = true
	}

	if renderCompose {
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

	prunedServices, pruneErr := r.pruneOrphanedServices(ctx, stackCfg, stackFile.Services)
	if pruneErr != nil {
		return result, wrapStackReconcileError("prune orphaned services", result.Services, pruneErr)
	}
	result.PrunedServices = prunedServices
	result.SourceDigest = digest

	return result, nil
}

func (r *stackReconciler) pruneOrphanedServices(
	ctx context.Context,
	stackCfg config.StackSpec,
	desiredServices []compose.Service,
) ([]string, error) {
	stackServices, err := r.services.ListStackServices(ctx, stackCfg.Name)
	if err != nil {
		return nil, err
	}

	desiredServiceNames := make(map[string]struct{}, len(desiredServices))
	for _, service := range desiredServices {
		desiredServiceNames[service.Name] = struct{}{}
	}

	prunedServices := make([]string, 0)
	for _, stackService := range stackServices {
		if _, exists := desiredServiceNames[stackService.Name]; exists {
			continue
		}
		if !isManagedService(stackService.Labels) {
			continue
		}

		pruneEnabled, resolveErr := resolveServicePrunePolicy(
			stackService.Labels,
			stackCfg,
			r.cfg.Spec.Sync.Policy,
		)
		if resolveErr != nil {
			return nil, fmt.Errorf(
				"resolve prune policy for service %s/%s: %w",
				stackCfg.Name,
				stackService.Name,
				resolveErr,
			)
		}
		if !pruneEnabled {
			continue
		}

		removeErr := r.services.Remove(ctx, stackService.ID)
		if removeErr != nil {
			if errors.Is(removeErr, swarm.ErrServiceNotFound) {
				continue
			}
			return nil, removeErr
		}

		slog.InfoContext(
			ctx,
			"[stack-reconciler] service pruned",
			slog.String("stack", stackCfg.Name),
			slog.String("service", stackService.Name),
		)
		prunedServices = append(prunedServices, stackService.Name)
	}

	sort.Strings(prunedServices)
	return prunedServices, nil
}

func isManagedService(labels map[string]string) bool {
	return strings.TrimSpace(labels[managedServiceLabelKey]) == managedServiceLabelValue
}

func resolveServicePrunePolicy(
	serviceLabels map[string]string,
	stackCfg config.StackSpec,
	globalPolicy config.SyncPolicySpec,
) (bool, error) {
	if rawValue, exists := serviceLabels[serviceSyncPolicyPruneLabelKey]; exists {
		value, err := strconv.ParseBool(strings.TrimSpace(rawValue))
		if err != nil {
			return false, fmt.Errorf(
				"parse label %q=%q: %w",
				serviceSyncPolicyPruneLabelKey,
				rawValue,
				err,
			)
		}

		return value, nil
	}

	if stackCfg.Sync.Policy.Prune != nil {
		return *stackCfg.Sync.Policy.Prune, nil
	}

	return globalPolicy.Prune, nil
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
