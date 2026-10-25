package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// Subscriber consumes one durable publication.
type Subscriber interface {
	// Handle applies the event; database repositories use the supplied context.
	Handle(context.Context, events.Envelope) error
}

type externalSubscriber struct{ Subscriber }

// External adapts a network consumer at the worker boundary. Its Handle runs
// without a database transaction; delivery still uses the same durable queue.
func External(handler Subscriber) Subscriber { return externalSubscriber{handler} }
