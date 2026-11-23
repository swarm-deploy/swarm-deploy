package tracing

import (
	"context"
	"log/slog"

	"github.com/cappuccinotm/slogx"
	"go.opentelemetry.io/otel/trace"
)

const traceIDLogAttribute = "trace-id"

// LogTraceID returns a middleware that adds the current trace ID to log records.
func LogTraceID() slogx.Middleware {
	return func(next slogx.HandleFunc) slogx.HandleFunc {
		return func(ctx context.Context, rec slog.Record) error {
			spanContext := trace.SpanContextFromContext(ctx)
			if spanContext.IsValid() {
				rec.AddAttrs(slog.String(traceIDLogAttribute, spanContext.TraceID().String()))
			}

			return next(ctx, rec)
		}
	}
}
