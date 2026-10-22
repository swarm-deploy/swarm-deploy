package pruner

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel/trace"
)

// ServicePruner removes managed services missing from desired stack state.
type ServicePruner struct {
	services swarm.ServiceManager
	event    dispatcher.Dispatcher
	syncCfg  config.SyncPolicySpec
}

// NewServicePruner builds a service pruner.
func NewServicePruner(
	serviceManager swarm.ServiceManager,
	eventDispatcher dispatcher.Dispatcher,
	syncCfg config.SyncPolicySpec,
) *ServicePruner {
	return &ServicePruner{
		services: serviceManager,
		event:    eventDispatcher,
		syncCfg:  syncCfg,
	}
}

// Prune deletes managed orphan services according to sync policy.
func (p *ServicePruner) Prune(
	ctx context.Context,
	req PruneServicesRequest,
) ([]string, error) {
	span := trace.SpanFromContext(ctx)

	desiredServiceNames := make(map[string]struct{}, len(req.Desired))
	for _, service := range req.Desired {
		desiredServiceNames[service.Name] = struct{}{}
	}

	prunedServices := make([]string, 0)
	for _, stackService := range req.Live {
		if _, exists := desiredServiceNames[stackService.Name]; exists {
			if span.IsRecording() {
				span.AddEvent("Live service found in desired state", trace.WithAttributes(
					tracing.ResourceServiceName.String(stackService.Name),
				))
			}

			continue
		}

		if !labelsdict.ServiceManaged(stackService.Labels) {
			if span.IsRecording() {
				span.AddEvent("Service skipped: unmanaged", trace.WithAttributes(
					tracing.ResourceServiceName.String(stackService.Name),
				))
			}
			continue
		}

		prunePolicyName, pruneEnabled := p.resolvePolicy(
			stackService.Labels,
			req.Stack,
		)
		if !pruneEnabled {
			span.AddEvent("Service skipped by sync policy", trace.WithAttributes(
				tracing.ResourcePrunePolicyName(prunePolicyName),
				tracing.ResourcePrunePolicyEnabled(pruneEnabled),
			))

			continue
		}

		span.SetAttributes(
			tracing.ResourcePrunePolicyName(prunePolicyName),
			tracing.ResourcePrunePolicyEnabled(pruneEnabled),
		)

		removeErr := p.services.Remove(ctx, stackService.ID)
		if removeErr != nil {
			if errors.Is(removeErr, swarm.ErrServiceNotFound) {
				span.AddEvent("Service not found")

				continue
			}
			return nil, removeErr
		}

		span.AddEvent("Service pruned")

		slog.InfoContext(
			ctx,
			"[service-pruner] service pruned",
			slog.String("stack", req.Stack.Name),
			slog.String("service", stackService.Name),
		)
		publishErr := p.event.Publish(ctx, &events.ServicePruned{
			StackName:   req.Stack.Name,
			ServiceName: stackService.Name,
			Commit:      req.Commit,
		})
		if publishErr != nil {
			return nil, publishErr
		}
		prunedServices = append(prunedServices, stackService.Name)
	}

	return prunedServices, nil
}

func (p *ServicePruner) resolvePolicy(serviceLabels map[string]string, stackCfg config.StackSpec) (string, bool) {
	if rawValue, exists := serviceLabels[labelsdict.ServiceSyncPolicyPruneLabelKey]; exists {
		return "service", strings.EqualFold(rawValue, "true")
	}

	if stackCfg.Sync.Policy.Prune != nil {
		return "stack", *stackCfg.Sync.Policy.Prune
	}

	return "global", p.syncCfg.Prune
}
