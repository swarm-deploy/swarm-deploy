package logx

import (
	"context"
	"log/slog"

	"github.com/cappuccinotm/slogx"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type eventTypeKey struct{}

func ContextWithEventType(ctx context.Context, eventType events.Type) context.Context {
	return context.WithValue(ctx, eventTypeKey{}, eventType)
}

func EventTypeFromContext(ctx context.Context) (events.Type, bool) {
	typ, ok := ctx.Value(eventTypeKey{}).(events.Type)
	if ok {
		return typ, true
	}
	return events.Type{}, false
}

func EventType() slogx.Middleware {
	return func(next slogx.HandleFunc) slogx.HandleFunc {
		return func(ctx context.Context, rec slog.Record) error {
			if typ, ok := EventTypeFromContext(ctx); ok {
				rec.AddAttrs(slog.String("event.type", typ.String()))
			}
			return next(ctx, rec)
		}
	}
}
