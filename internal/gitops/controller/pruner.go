package controller

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type ServicePruner struct {
	services swarm.ServiceManager
	syncCfg  config.SyncPolicySpec
}

func NewServicePruner(
	serviceManager swarm.ServiceManager,
	syncCfg config.SyncPolicySpec,
) *ServicePruner {
	return &ServicePruner{
		services: serviceManager,
		syncCfg:  syncCfg,
	}
}

func (p *ServicePruner) Prune(
	ctx context.Context,
	stackCfg config.StackSpec,
	desiredServices []compose.Service,
) ([]string, error) {
	stackServices, err := p.services.ListStackServices(ctx, stackCfg.Name)
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

		pruneEnabled := p.resolvePolicy(
			stackService.Labels,
			stackCfg,
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
			slog.String("stack", stackCfg.Name),
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
