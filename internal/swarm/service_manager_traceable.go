package swarm

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type traceableServiceManager struct {
	tracer         trace.Tracer
	serviceManager ServiceManager
}

func traceServiceManager(tracer trace.Tracer, serviceManager ServiceManager) ServiceManager {
	return &traceableServiceManager{
		tracer:         tracer,
		serviceManager: serviceManager,
	}
}

func (t traceableServiceManager) GetReplicas(ctx context.Context, serviceRef ServiceReference) (uint64, error) {
	ctx, span := t.startServiceSpan(ctx, "swarm.GetServiceReplicas", serviceRef)
	defer span.End()

	replicas, err := t.serviceManager.GetReplicas(ctx, serviceRef)
	if err != nil {
		tracing.FailSpan(span, err)
		return 0, err
	}

	span.SetAttributes(tracing.ResourceServiceReplicas.Int64(int64(replicas))) //nolint:gosec // nn
	span.SetStatus(codes.Ok, "")

	return replicas, nil
}

func (t traceableServiceManager) ListStackServices(ctx context.Context, stackName string) ([]StackService, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.ListStackServices", trace.WithAttributes(
		tracing.ResourceStackName.String(stackName),
	))
	defer span.End()

	services, err := t.serviceManager.ListStackServices(ctx, stackName)
	if err != nil {
		tracing.FailSpan(span, err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("services.count", len(services)))
	span.SetStatus(codes.Ok, "")

	return services, nil
}

func (t traceableServiceManager) Remove(ctx context.Context, serviceIDOrName string) error {
	ctx, span := t.tracer.Start(ctx, "swarm.RemoveService", trace.WithAttributes(
		tracing.ResourceServiceID.String(serviceIDOrName),
	))
	defer span.End()

	err := t.serviceManager.Remove(ctx, serviceIDOrName)
	if err != nil {
		tracing.FailSpan(span, err)
		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

func (t traceableServiceManager) Scale(ctx context.Context, serviceRef ServiceReference, replicas uint64) error {
	ctx, span := t.startServiceSpan(ctx, "swarm.ScaleService", serviceRef)
	defer span.End()

	span.SetAttributes(tracing.ResourceServiceReplicas.Int64(int64(replicas))) //nolint:gosec // nn

	err := t.serviceManager.Scale(ctx, serviceRef, replicas)
	if err != nil {
		tracing.FailSpan(span, err)
		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

func (t traceableServiceManager) Restart(ctx context.Context, serviceRef ServiceReference) (uint64, error) {
	ctx, span := t.startServiceSpan(ctx, "swarm.RestartService", serviceRef)
	defer span.End()

	replicas, err := t.serviceManager.Restart(ctx, serviceRef)
	if err != nil {
		tracing.FailSpan(span, err)
		return 0, err
	}

	span.SetAttributes(tracing.ResourceServiceReplicas.Int64(int64(replicas))) //nolint:gosec // nn
	span.SetStatus(codes.Ok, "")

	return replicas, nil
}

func (t traceableServiceManager) GetStatus(ctx context.Context, serviceRef ServiceReference) (ServiceStatus, error) {
	ctx, span := t.startServiceSpan(ctx, "swarm.GetServiceStatus", serviceRef)
	defer span.End()

	status, err := t.serviceManager.GetStatus(ctx, serviceRef)
	if err != nil {
		tracing.FailSpan(span, err)
		return ServiceStatus{}, err
	}

	span.SetStatus(codes.Ok, "")

	return status, nil
}

func (t traceableServiceManager) ListTasks(ctx context.Context, serviceRef ServiceReference) ([]ServiceTask, error) {
	ctx, span := t.startServiceSpan(ctx, "swarm.ListTasks", serviceRef)
	defer span.End()

	tasks, err := t.serviceManager.ListTasks(ctx, serviceRef)
	if err != nil {
		tracing.FailSpan(span, err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("tasks.count", len(tasks)))
	span.SetStatus(codes.Ok, "")

	return tasks, nil
}

func (t traceableServiceManager) Get(ctx context.Context, serviceRef ServiceReference) (Service, error) {
	ctx, span := t.startServiceSpan(ctx, "swarm.GetService", serviceRef)
	defer span.End()

	service, err := t.serviceManager.Get(ctx, serviceRef)
	if err != nil {
		tracing.FailSpan(span, err)
		return Service{}, err
	}

	span.SetAttributes(tracing.ResourceServiceID.String(service.ID))
	span.SetStatus(codes.Ok, "")

	return service, nil
}

func (t traceableServiceManager) Logs(
	ctx context.Context,
	serviceRef ServiceReference,
	options ServiceLogsOptions,
) ([]string, error) {
	ctx, span := t.startServiceSpan(ctx, "swarm.Logs", serviceRef)
	defer span.End()

	span.SetAttributes(attribute.Int("logs.limit", options.Limit))
	t.setTimeAttribute(span, "logs.since", options.Since)
	t.setTimeAttribute(span, "logs.until", options.Until)

	logs, err := t.serviceManager.Logs(ctx, serviceRef, options)
	if err != nil {
		tracing.FailSpan(span, err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("logs.count", len(logs)))
	span.SetStatus(codes.Ok, "")

	return logs, nil
}

func (t traceableServiceManager) startServiceSpan(
	ctx context.Context,
	name string,
	serviceRef ServiceReference,
) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, name, trace.WithAttributes(
		tracing.ResourceStackName.String(serviceRef.StackName()),
		tracing.ResourceServiceName.String(serviceRef.ServiceName()),
	))
}

func (t traceableServiceManager) setTimeAttribute(span trace.Span, key string, value *time.Time) {
	if value == nil {
		return
	}

	span.SetAttributes(attribute.String(key, value.Format(time.RFC3339Nano)))
}
