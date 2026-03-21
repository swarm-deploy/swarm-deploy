package dispatcher

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/event/events"
)

type Event interface {
	Type() events.Type
}

type Dispatcher interface {
	Dispatch(ctx context.Context, event Event)
	Shutdown(ctx context.Context) error
}

type NopDispatcher struct{}

func (*NopDispatcher) Dispatch(_ context.Context, _ Event) {}
func (*NopDispatcher) Shutdown(context.Context) error      { return nil }
