package tracing

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func ReadFile() func(ctx context.Context, path string) ([]byte, error) {
	if !tracingEnabled {
		return func(_ context.Context, path string) ([]byte, error) {
			return os.ReadFile(path)
		}
	}

	tracer := otel.GetTracerProvider().Tracer("github.com/swarm-deploy/swarm-deploy/internal/tracing")

	return func(ctx context.Context, path string) ([]byte, error) {
		ctx, span := tracer.Start(ctx, "os.ReadFile", trace.WithAttributes(
			attribute.String("file.path", path),
		))
		defer span.End()

		content, err := os.ReadFile(path)
		if err != nil {
			FailSpan(span, err)

			return content, err
		}

		span.SetStatus(codes.Ok, "")

		return content, nil
	}
}
