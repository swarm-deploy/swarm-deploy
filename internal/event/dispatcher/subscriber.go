package dispatcher

import (
	"context"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/logx"
)

type Subscriber interface {
	// Name return the subscriber name. Useful for logging purposes.
	Name() string
	Slow() bool
	Handle(ctx context.Context, event events.Event) error
}

func handleSubscriber(subscriber Subscriber, event events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultSubscribeHandleTimeout)
	defer cancel()

	ctx = logx.ContextWithEventType(ctx, event.Type())

	slog.DebugContext(ctx, "[event] running subscriber",
		slog.String("subscriber.name", subscriber.Name()),
	)

	err := subscriber.Handle(context.Background(), event)
	if err != nil {
		slog.WarnContext(ctx, "[event] subscriber failed", slog.Any("err", err))
	}
}
