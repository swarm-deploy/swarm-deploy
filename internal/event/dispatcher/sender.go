package dispatcher

import (
	"context"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/logx"
	"github.com/swarm-deploy/swarm-deploy/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type EventSender func(ctx context.Context, event events.Event, sub Subscriber) error

func directEventSender() EventSender {
	return func(ctx context.Context, event events.Event, sub Subscriber) error {
		ctx = logx.ContextWithEventType(ctx, event.Type())

		slog.DebugContext(ctx, "[event] running subscriber",
			slog.String("subscriber.name", sub.Name()),
		)

		err := sub.Handle(ctx, event)
		if err != nil {
			slog.WarnContext(ctx, "[event] subscriber failed", slog.Any("err", err))
			return err
		}

		return nil
	}
}

func createEventSender() EventSender {
	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return directEventSender()
	}

	return traceEventSender(tp, directEventSender())
}

func traceEventSender(tp trace.TracerProvider, sender EventSender) EventSender {
	tracer := tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher")

	return func(ctx context.Context, event events.Event, sub Subscriber) error {
		ctx, span := tracer.Start(ctx, "dispatcher.Send", trace.WithAttributes(
			attribute.String("event.name", string(event.Type().Name())),
			attribute.String("subscriber.name", sub.Name()),
		))
		defer span.End()

		err := sender(ctx, event, sub)
		if err != nil {
			tracing.FailSpan(span, err)
			return err
		}

		return nil
	}
}
