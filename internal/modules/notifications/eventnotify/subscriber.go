package eventnotify

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/actions"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
)

type Subscriber struct {
	notifier      notifiers.Notifier
	actionBuilder actions.Builder
}

func NewSubscriber(
	notifier notifiers.Notifier, actionBuilder actions.Builder,
) *Subscriber {
	return &Subscriber{
		notifier: notifier, actionBuilder: actionBuilder,
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
	message := notifiers.Message{Payload: payload}
	if deployment, ok := payload.(DeploymentPayload); ok {
		message.Actions = s.actionBuilder.Deployment(deployment.DeploymentID)
	}
	_, err = s.notifier.Notify(ctx, message)
	return err
}
