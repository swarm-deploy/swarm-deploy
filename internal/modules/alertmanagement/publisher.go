package alertmanagement

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// Publisher publishes alert lifecycle events.
type Publisher interface {
	// Publish emits an event using the caller's transaction context.
	Publish(ctx context.Context, event events.Event) error
}
