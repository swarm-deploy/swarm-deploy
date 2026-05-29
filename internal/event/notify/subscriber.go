package notify

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/notifiers"
)

type Subscriber struct {
	notifier   notifiers.Notifier
	dispatcher dispatcher.Dispatcher
}

func NewSubscriber(
	notifier notifiers.Notifier,
	dispatcher dispatcher.Dispatcher,
) *Subscriber {
	return &Subscriber{
		notifier:   notifier,
		dispatcher: dispatcher,
	}
}

func (s *Subscriber) Name() string {
	return s.notifier.Name()
}

func (s *Subscriber) Slow() bool {
	return true
}

func (s *Subscriber) Handle(ctx context.Context, event events.Event) error {
	err := s.notifier.Notify(ctx, notifiers.Message{
		Payload: event,
	})
	if err == nil {
		return nil
	}

	if event.Type() != events.TypeSendNotificationFailed {
		s.dispatcher.Dispatch(ctx, &events.SendNotificationFailed{
			EventType:   event.Type(),
			Destination: s.notifier.Kind(),
			Channel:     s.notifier.Name(),
			Error:       err,
		})
	}

	return err
}
