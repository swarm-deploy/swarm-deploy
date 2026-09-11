package traceos

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/swarm-deploy/swarm-deploy/internal/tracing"
)

func ReadFile() func(ctx context.Context, path string) ([]byte, error) {
	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return func(_ context.Context, path string) ([]byte, error) {
			return os.ReadFile(path)
		}
	}

	tracer := tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/tracing")

	return func(ctx context.Context, path string) ([]byte, error) {
		ctx, span := tracer.Start(ctx, "os.ReadFile", trace.WithAttributes(
			attribute.String("file.path", path),
		))
		defer span.End()

		content, err := os.ReadFile(path)
		if err != nil {
			tracing.FailSpan(span, err)

			return content, err
		}

		span.SetStatus(codes.Ok, "")

		return content, nil
	}
}
