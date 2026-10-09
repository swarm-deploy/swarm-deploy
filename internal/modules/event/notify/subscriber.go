package notify

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
)

type Subscriber struct {
	notifier notifiers.Notifier
}

func NewSubscriber(
	notifier notifiers.Notifier,
) *Subscriber {
	return &Subscriber{
		notifier: notifier,
	}
}

func (s *Subscriber) Name() string {
	return s.notifier.Name()
}

func (s *Subscriber) Handle(ctx context.Context, envelope events.Envelope) error {
	return s.notifier.Notify(ctx, notifiers.Message{
		Payload: envelope.Event,
	})
}
