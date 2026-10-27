//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mocks.go -package=dispatcher
package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

type Dispatcher interface {
	// Subscribe registers a subscriber for event type.
	Subscribe(eventType events.TypeName, id string, subscriber Subscriber) error

	// Publish persists work without invoking subscribers.
	Publish(ctx context.Context, event events.Event) error
}

type NopDispatcher struct{}

func (*NopDispatcher) Subscribe(events.TypeName, string, Subscriber) error { return nil }
func (*NopDispatcher) Publish(context.Context, events.Event) error         { return nil }

// SubscribeMany registers the same stable subscriber for several event types.
// Call during module initialization, before any workers start.
func SubscribeMany(eventBus Dispatcher, types []events.TypeName, id string, subscriber Subscriber) error {
	for _, typ := range types {
		if err := eventBus.Subscribe(typ, id, subscriber); err != nil {
			return err
		}
	}
	return nil
}
