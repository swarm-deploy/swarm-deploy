package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type Subscriber interface {
	// Name return the subscriber name. Useful for logging purposes.
	Name() string
	Slow() bool
	Handle(ctx context.Context, event events.Event) error
}
