package notify

import (
	"context"
	"fmt"

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
	payload, err := notificationPayload(envelope)
	if err != nil {
		return fmt.Errorf("build notification payload: %w", err)
	}
	_, err = s.notifier.Notify(ctx, notifiers.Message{
		Payload: payload,
	})
	return err
}
