package storage

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type skipSQLTracingKey struct{}

// WithoutSQLTracing suppresses driver spans for SQL operations using the returned context.
func WithoutSQLTracing(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipSQLTracingKey{}, true)
}

type sqlTracerProvider struct{ trace.TracerProvider }

func (p sqlTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return sqlTracer{p.TracerProvider.Tracer(name, options...)}
}

type sqlTracer struct{ trace.Tracer }

func (t sqlTracer) Start(
	ctx context.Context, name string, options ...trace.SpanStartOption,
) (context.Context, trace.Span) {
	if ctx.Value(skipSQLTracingKey{}) == true {
		return ctx, noop.Span{}
	}
	return t.Tracer.Start(ctx, name, options...)
}
