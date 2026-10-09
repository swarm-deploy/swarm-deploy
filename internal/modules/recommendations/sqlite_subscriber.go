package recommendations

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

type orderedSubscriber struct {
	db   *storage.Database
	next outbox.Subscriber
}

func (s *orderedSubscriber) Handle(ctx context.Context, event events.Envelope) error {
	var stack string
	switch e := event.Event.(type) {
	case *events.DeploySuccess:
		stack = e.StackName
	case *events.DeployFailed:
		stack = e.StackName
	default:
		return nil
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		accepted, err := storage.AdvanceProjection(ctx, s.db.Get, "recommendations", stack, event.OccurredAt, event.ID)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		return s.next.Handle(ctx, event)
	})
}
