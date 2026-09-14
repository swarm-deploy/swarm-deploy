package swarm

import (
	"context"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type traceableConfigManager struct {
	tracer        trace.Tracer
	configManager ConfigManager
}

func traceConfigManager(tracer trace.Tracer, configManager ConfigManager) ConfigManager {
	return &traceableConfigManager{
		tracer:        tracer,
		configManager: configManager,
	}
}

func (t traceableConfigManager) Get(ctx context.Context, configName string) (Config, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.GetConfig", trace.WithAttributes(
		tracing.ResourceConfigName.String(configName),
	))
	defer span.End()

	config, err := t.configManager.Get(ctx, configName)
	if err != nil {
		tracing.FailSpan(span, err)
		return Config{}, err
	}

	span.SetAttributes(
		tracing.ResourceConfigID.String(config.ID),
		tracing.ResourceConfigName.String(config.Name),
	)
	span.SetStatus(codes.Ok, "")

	return config, nil
}

func (t traceableConfigManager) ResolveReference(
	ctx context.Context,
	source,
	target string,
) (*dockerswarm.ConfigReference, error) {
	return t.configManager.ResolveReference(ctx, source, target)
}
