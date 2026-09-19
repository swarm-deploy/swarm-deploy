//go:generate mockgen -source=$GOFILE -destination=mocks.go -package=dispatcher
package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type Dispatcher interface {
	// Subscribe registers a subscriber for event type.
	Subscribe(eventType events.Type, subscriber Subscriber)

	// Dispatch schedules an event for subscribed handlers.
	Dispatch(ctx context.Context, event events.Event)
}

type NopDispatcher struct{}

func (*NopDispatcher) Subscribe(events.Type, Subscriber)          {}
func (*NopDispatcher) Dispatch(_ context.Context, _ events.Event) {}
