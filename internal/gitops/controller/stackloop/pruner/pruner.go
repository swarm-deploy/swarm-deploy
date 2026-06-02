package pruner

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ServicePruner removes managed services missing from desired stack state.
type ServicePruner struct {
	services swarm.ServiceManager
	syncCfg  config.SyncPolicySpec
}

// NewServicePruner builds a service pruner.
func NewServicePruner(
	serviceManager swarm.ServiceManager,
	syncCfg config.SyncPolicySpec,
) *ServicePruner {
	return &ServicePruner{
		services: serviceManager,
		syncCfg:  syncCfg,
	}
}

// Prune deletes managed orphan services according to sync policy.
func (p *ServicePruner) Prune(
	ctx context.Context,
	req PruneServicesRequest,
) ([]string, error) {
	desiredServiceNames := make(map[string]struct{}, len(req.Desired))
	for _, service := range req.Desired {
		desiredServiceNames[service.Name] = struct{}{}
	}

	prunedServices := make([]string, 0)
	for _, stackService := range req.Live {
		if _, exists := desiredServiceNames[stackService.Name]; exists {
			continue
		}
		if !labelsdict.ServiceManaged(stackService.Labels) {
			continue
		}

		pruneEnabled := p.resolvePolicy(
			stackService.Labels,
			req.Stack,
		)
		if !pruneEnabled {
			continue
		}

		removeErr := p.services.Remove(ctx, stackService.ID)
		if removeErr != nil {
			if errors.Is(removeErr, swarm.ErrServiceNotFound) {
				continue
			}
			return nil, removeErr
		}

		slog.InfoContext(
			ctx,
			"[service-pruner] service pruned",
			slog.String("stack", req.Stack.Name),
			slog.String("service", stackService.Name),
		)
		prunedServices = append(prunedServices, stackService.Name)
	}

	return prunedServices, nil
}

func (p *ServicePruner) resolvePolicy(serviceLabels map[string]string, stackCfg config.StackSpec) bool {
	if rawValue, exists := serviceLabels[labelsdict.ServiceSyncPolicyPruneLabelKey]; exists {
		return strings.EqualFold(rawValue, "true")
	}

	if stackCfg.Sync.Policy.Prune != nil {
		return *stackCfg.Sync.Policy.Prune
	}

	return p.syncCfg.Prune
}
