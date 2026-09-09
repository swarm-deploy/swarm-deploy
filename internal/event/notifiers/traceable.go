package notifiers

import (
	"context"

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
			"github.com/swarm-deploy/swarm-deploy/internal/event/notifier",
			trace.WithInstrumentationAttributes(attrs...),
		),
	}
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
