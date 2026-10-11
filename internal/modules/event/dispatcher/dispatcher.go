//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mocks.go -package=dispatcher
package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
)

type Dispatcher interface {
	// Subscribe registers a subscriber for event type.
	Subscribe(eventType events.TypeName, id string, subscriber outbox.Subscriber) error

	// Publish persists work without invoking subscribers.
	Publish(ctx context.Context, event events.Event) error
}

type NopDispatcher struct{}

func (*NopDispatcher) Subscribe(events.TypeName, string, outbox.Subscriber) error { return nil }
func (*NopDispatcher) Publish(context.Context, events.Event) error                { return nil }
