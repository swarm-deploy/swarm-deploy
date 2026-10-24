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
	_, err := s.HandleNotification(ctx, envelope)
	return err
}

// HandleNotification reports success only after the notifier returns successfully.
func (s *Subscriber) HandleNotification(ctx context.Context, envelope events.Envelope) (bool, error) {
	payload, err := notificationPayload(envelope)
	if err != nil {
		return false, fmt.Errorf("build notification payload: %w", err)
	}
	_, err = s.notifier.Notify(ctx, notifiers.Message{Payload: payload})
	return err == nil, err
}
