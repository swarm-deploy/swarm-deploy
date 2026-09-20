package sd

import (
	"context"
	"fmt"
	"strings"

	downwardotel "github.com/swarm-deploy/downward-otel/go"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/buildinfo"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type BuildInfo struct {
	Version string
}

// InitTracerProvider initializes the global OpenTelemetry tracer provider.
func InitTracerProvider(
	ctx context.Context,
	cfg *config.TracingSpec,
	info buildinfo.Info,
) (*sdktrace.TracerProvider, error) {
	if cfg == nil {
		return nil, nil //nolint:nilnil // check outside
	}

	exporter, err := buildExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(
		ctx,
		resource.WithDetectors(downwardotel.NewDetector()),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			tracing.ServiceVersion.String(info.Version),
			tracing.ServiceBuildTime.String(info.Date),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build OpenTelemetry resource: %w", err)
	}

	providerOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	}
	if sampler := buildSampler(cfg.Sampler); sampler != nil {
		providerOptions = append(providerOptions, sdktrace.WithSampler(sampler))
	}

	provider := sdktrace.NewTracerProvider(providerOptions...)

	tracing.Enable()

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return provider, nil
}

func buildSampler(cfg config.TracingSamplerSpec) sdktrace.Sampler {
	switch strings.TrimSpace(cfg.Strategy.Value) {
	case "":
		return nil
	case config.TracingSamplerAlwaysOn:
		return sdktrace.AlwaysSample()
	case config.TracingSamplerAlwaysOff:
		return sdktrace.NeverSample()
	case config.TracingSamplerTraceIDRatio:
		return sdktrace.TraceIDRatioBased(cfg.Ratio.Value)
	case config.TracingSamplerParentBasedTraceIDRatio:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.Ratio.Value))
	default:
		return nil
	}
}

func buildExporter(ctx context.Context, cfg *config.TracingSpec) (sdktrace.SpanExporter, error) {
	headers := make(map[string]string, len(cfg.Exporter.Headers)+2) //nolint:mnd // looks at const
	for key, value := range cfg.Exporter.Headers {
		headers[key] = value.Value
	}

	auth := cfg.Exporter.Authentication

	bearer := strings.TrimSpace(string(auth.Bearer.Content))
	if bearer != "" {
		headers["Authorization"] = "Bearer " + bearer
	}

	apiKey := strings.TrimSpace(string(auth.APIKey.Content))
	if apiKey != "" {
		headers["Authorization"] = "Api-Key " + apiKey
	}

	xAPIKey := strings.TrimSpace(string(auth.XAPIKey.Content))
	if xAPIKey != "" {
		headers["x-api-key"] = xAPIKey
	}

	endpoint := strings.TrimSpace(cfg.Exporter.Endpoint.Value)

	switch cfg.Transport {
	case config.TracingTransportHTTP:
		exporter, err := otlptracehttp.New(
			ctx,
			otlptracehttp.WithEndpointURL(endpoint),
			otlptracehttp.WithHeaders(headers),
		)
		if err != nil {
			return nil, fmt.Errorf("build OTLP/HTTP trace exporter: %w", err)
		}
		return exporter, nil
	case config.TracingTransportGRPC:
		exporter, err := otlptracegrpc.New(
			ctx,
			otlptracegrpc.WithEndpointURL(endpoint),
			otlptracegrpc.WithHeaders(headers),
		)
		if err != nil {
			return nil, fmt.Errorf("build OTLP/gRPC trace exporter: %w", err)
		}
		return exporter, nil
	default:
		return nil, fmt.Errorf("unsupported tracing transport %q", cfg.Transport)
	}
}
