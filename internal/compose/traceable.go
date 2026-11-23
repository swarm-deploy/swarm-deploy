package compose

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TraceableFileLoader records compose file loading spans.
type TraceableFileLoader struct {
	tracer trace.Tracer
	loader FileLoader
}

// NewTraceableFileLoader wraps a compose file loader with tracing.
func NewTraceableFileLoader(tp trace.TracerProvider, loader FileLoader) FileLoader {
	return &TraceableFileLoader{
		tracer: tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/compose"),
		loader: loader,
	}
}

// Load reads a compose file and records the operation result in a span.
func (t *TraceableFileLoader) Load(ctx context.Context, path string) (*File, error) {
	ctx, span := t.tracer.Start(ctx, "compose.Load", trace.WithAttributes(
		tracing.ResourceComposePath.String(path),
	))
	defer span.End()

	file, err := t.loader.Load(ctx, path)
	if err != nil {
		tracing.FailSpan(span, err)

		return file, err
	}

	span.SetAttributes(tracing.ResourceComposeDigest.String(file.Digest))

	span.SetStatus(codes.Ok, "")

	return file, nil
}
