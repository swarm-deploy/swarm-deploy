package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type Dispatcher interface {
	// Subscribe registers a subscriber for event type.
	Subscribe(eventType events.Type, subscriber Subscriber)

	Dispatch(ctx context.Context, event events.Event)
	Shutdown(ctx context.Context) error
}

type NopDispatcher struct{}

func (*NopDispatcher) Subscribe(events.Type, Subscriber)          {}
func (*NopDispatcher) Dispatch(_ context.Context, _ events.Event) {}
func (*NopDispatcher) Shutdown(context.Context) error             { return nil }
