package notifiers

import (
	"context"
	"net/http"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TraceableNotifier struct {
	notifier Notifier
	tracer   trace.Tracer
}

func NewTraceableNotifier(notifier Notifier, tp trace.TracerProvider, attrs []attribute.KeyValue) Notifier {
	attrs = append(attrs, attribute.String("notifier.name", notifier.Name()))
	attrs = append(attrs, attribute.String("notifier.kind", notifier.Kind()))

	return &TraceableNotifier{
		notifier: notifier,
		tracer: tp.Tracer(
			"github.com/swarm-deploy/swarm-deploy/internal/event/notifiers",
			trace.WithInstrumentationAttributes(attrs...),
		),
	}
}

func traceTransport(transport http.RoundTripper, path string) http.RoundTripper {
	if !tracing.Enabled() {
		return transport
	}

	opts := []otelhttp.Option{}
	if path != "" {
		opts = append(opts, otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return "HTTP " + r.Method + " " + path
		}))
	}

	return otelhttp.NewTransport(transport, opts...)
}

func (t *TraceableNotifier) Name() string {
	return t.notifier.Name()
}

func (t *TraceableNotifier) Kind() string {
	return t.notifier.Kind()
}

func (t *TraceableNotifier) Notify(ctx context.Context, event Message) error {
	ctx, span := t.tracer.Start(ctx, "notifier.Notify")
	defer span.End()

	err := t.notifier.Notify(ctx, event)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "OK")

	return nil
}
