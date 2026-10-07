package swarm

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type traceableNetworkManager struct {
	tracer         trace.Tracer
	networkManager NetworkManager
}

func traceNetworkManager(tracer trace.Tracer, networkManager NetworkManager) NetworkManager {
	return &traceableNetworkManager{
		tracer:         tracer,
		networkManager: networkManager,
	}
}

func (t traceableNetworkManager) Get(ctx context.Context, name string) (Network, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.GetNetwork", trace.WithAttributes(
		tracing.ResourceNetworkName.String(name),
	))
	defer span.End()

	network, err := t.networkManager.Get(ctx, name)
	if err != nil {
		tracing.FailSpan(span, err)
		return Network{}, err
	}

	span.SetAttributes(tracing.ResourceNetworkID.String(network.ID))
	span.SetStatus(codes.Ok, "")

	return network, nil
}

func (t traceableNetworkManager) List(ctx context.Context) ([]Network, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.ListNetworks")
	defer span.End()

	networks, err := t.networkManager.List(ctx)
	if err != nil {
		tracing.FailSpan(span, err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")

	return networks, nil
}

func (t traceableNetworkManager) Map(ctx context.Context, ids []string) (map[string]Network, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.MapNetworks", trace.WithAttributes(
		tracing.ResourceNetworkID.StringSlice(ids),
	))
	defer span.End()

	networks, err := t.networkManager.Map(ctx, ids)
	if err != nil {
		tracing.FailSpan(span, err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")

	return networks, nil
}

func (t traceableNetworkManager) Create(ctx context.Context, req CreateNetworkRequest) (string, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.CreateNetwork", trace.WithAttributes(
		tracing.ResourceNetworkName.String(req.Name),
	))
	defer span.End()

	id, err := t.networkManager.Create(ctx, req)
	if err != nil {
		tracing.FailSpan(span, err)
		return "", err
	}

	span.SetAttributes(
		tracing.ResourceNetworkID.String(id),
	)
	span.SetStatus(codes.Ok, "")

	return id, nil
}
