package dispatcher

import (
	"context"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/event/logx"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type EventSender func(ctx context.Context, msg message) error

func directEventSender() EventSender {
	return func(ctx context.Context, msg message) error {
		ctx = logx.ContextWithEventType(ctx, msg.Event.Type())

		slog.DebugContext(ctx, "[event] running subscriber",
			slog.String("subscriber.name", msg.Subscriber.Name()),
		)

		err := msg.Subscriber.Handle(ctx, msg.Event)
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

	return func(ctx context.Context, msg message) error {
		ctx = trace.ContextWithSpanContext(ctx, msg.SpanContext)

		ctx, span := tracer.Start(ctx, "dispatcher.Send", trace.WithAttributes(
			attribute.String("event.name", string(msg.Event.Type().Name())),
			attribute.String("subscriber.name", msg.Subscriber.Name()),
		))
		defer span.End()

		err := sender(ctx, msg)
		if err != nil {
			tracing.FailSpan(span, err)
			return err
		}

		return nil
	}
}
