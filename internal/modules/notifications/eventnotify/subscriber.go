package eventnotify

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
	if success, ok := envelope.Event.(*events.DeploySuccess); ok && len(success.Services) == 0 {
		// An Attempt without targets only prunes; ServicePruned reports each removal.
		return nil
	}
	payload, err := notificationPayload(envelope)
	if err != nil {
		return fmt.Errorf("build notification payload: %w", err)
	}
	_, err = s.notifier.Notify(ctx, notifiers.Message{
		Payload: payload,
	})
	return err
}
