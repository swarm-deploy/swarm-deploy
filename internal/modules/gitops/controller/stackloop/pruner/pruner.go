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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// ServicePruner removes managed services missing from desired stack state.
type ServicePruner struct {
	services swarm.ServiceManager
	event    dispatcher.Dispatcher
	syncCfg  config.SyncPolicySpec
	tracer   trace.Tracer
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
		tracer:   otel.Tracer("github.com/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"),
	}
}

// Prune deletes managed orphan services according to sync policy.
func (p *ServicePruner) Prune(
	ctx context.Context,
	req PruneServicesRequest,
) ([]string, error) {
	span := trace.SpanFromContext(ctx)

	desiredServiceNames := make(map[string]struct{}, len(req.Desired))
	desiredServiceNamesList := make([]string, 0, len(req.Desired))
	liveServiceNames := make([]string, 0, len(req.Live))

	for _, service := range req.Desired {
		desiredServiceNames[service.Name] = struct{}{}
		desiredServiceNamesList = append(desiredServiceNamesList, service.Name)
	}

	span.SetAttributes(tracing.ResourceDesiredServiceNames(desiredServiceNamesList))

	prunedServices := make([]string, 0)
	for _, liveService := range req.Live {
		liveServiceNames = append(liveServiceNames, liveService.Name)

		if _, exists := desiredServiceNames[liveService.Name]; exists {
			span.AddEvent("Service skipped: service exists in desired state", trace.WithAttributes(
				tracing.ResourceServiceName.String(liveService.Name),
			))

			continue
		}

		pruned, err := p.pruneService(ctx, liveService, req.Stack, req.Commit)
		if err != nil {
			slog.ErrorContext(ctx, "[service-pruner] failed to prune service",
				slog.Any("err", err),
				slog.String("stack.name", req.Stack.Name),
				slog.String("service.name", liveService.Name),
			)

			tracing.FailSpan(span, err)
		}

		if pruned {
			span.AddEvent("Service pruned", trace.WithAttributes(
				tracing.ResourceServiceName.String(liveService.Name),
			))

			prunedServices = append(prunedServices, liveService.Name)
		}
	}

	span.SetAttributes(
		tracing.ResourceLiveServiceNames(liveServiceNames),
	)

	return prunedServices, nil
}

func (p *ServicePruner) pruneService(
	ctx context.Context,
	service swarm.StackService,
	stackCfg config.StackSpec,
	commit string,
) (bool, error) {
	ctx, span := p.tracer.Start(ctx, "pruner.PruneService", trace.WithAttributes(
		tracing.ResourceServiceName.String(service.Name),
	))
	defer span.End()

	if !labelsdict.ServiceManaged(service.Labels) {
		span.AddEvent("Service skipped: service unmanaged")
		return false, nil
	}

	prunePolicyName, pruneEnabled := p.resolvePolicy(
		service.Labels,
		stackCfg,
	)
	if !pruneEnabled {
		span.AddEvent("Service skipped by sync policy", trace.WithAttributes(
			tracing.ResourcePrunePolicyName(prunePolicyName),
			tracing.ResourcePrunePolicyEnabled(pruneEnabled),
		))
		return false, nil
	}

	span.SetAttributes(
		tracing.ResourcePrunePolicyName(prunePolicyName),
		tracing.ResourcePrunePolicyEnabled(pruneEnabled),
	)

	removeErr := p.services.Remove(ctx, service.ID)
	if removeErr != nil {
		if errors.Is(removeErr, swarm.ErrServiceNotFound) {
			span.AddEvent("Service not found")

			return false, nil
		}

		tracing.FailSpan(span, removeErr)

		return false, removeErr
	}

	span.AddEvent("Service pruned")

	slog.InfoContext(
		ctx,
		"[service-pruner] service pruned",
		slog.String("stack", stackCfg.Name),
		slog.String("service", service.Name),
	)
	publishErr := p.event.Publish(ctx, &events.ServicePruned{
		StackName:   stackCfg.Name,
		ServiceName: service.Name,
		Commit:      commit,
	})
	if publishErr != nil {
		slog.WarnContext(ctx, "[service-pruner] failed to publish event", slog.Any("err", publishErr))
		tracing.FailSpan(span, publishErr)
	}

	return true, nil
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
