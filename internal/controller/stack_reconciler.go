package controller

import (
	"context"
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
	services stackServiceManager
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

		prunedServices, pruneErr := r.pruneOrphanedServices(ctx, stackCfg, stackFile.Services)
		if pruneErr != nil {
			return result, wrapStackReconcileError("prune orphaned services", result.Services, pruneErr)
		}
		result.PrunedServices = prunedServices

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

	prunedServices, pruneErr := r.pruneOrphanedServices(ctx, stackCfg, stackFile.Services)
	if pruneErr != nil {
		return result, wrapStackReconcileError("prune orphaned services", result.Services, pruneErr)
	}
	result.PrunedServices = prunedServices
	result.SourceDigest = stackFile.Digest
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
