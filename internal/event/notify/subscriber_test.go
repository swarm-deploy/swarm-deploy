package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/notifiers"
)

type testNotifier struct {
	name      string
	kind      string
	err       error
	lastEvent notifiers.Message
}

type testDispatcher struct {
	lastEvent events.Event
}

func (d *testDispatcher) Dispatch(_ context.Context, event events.Event) {
	d.lastEvent = event
}

func (*testDispatcher) Subscribe(events.Type, dispatcher.Subscriber) {}

func (*testDispatcher) Shutdown(context.Context) error {
	return nil
}

func (n *testNotifier) Name() string {
	return n.name
}

func (n *testNotifier) Kind() string {
	return n.kind
}

func (n *testNotifier) Notify(_ context.Context, event notifiers.Message) error {
	n.lastEvent = event
	return n.err
}

func TestSubscriberHandleDispatchesNotificationFailureEvent(t *testing.T) {
	dispatchErr := errors.New("telegram timeout")
	notifier := &testNotifier{
		name: "ops",
		kind: "telegram",
		err:  dispatchErr,
	}
	dispatcher := &testDispatcher{}

	sub := NewSubscriber(notifier, dispatcher)

	sourceEvent := &events.DeploySuccess{StackName: "api", Commit: "abc"}
	err := sub.Handle(context.Background(), sourceEvent)
	require.Error(t, err, "expected notify error")
	assert.ErrorIs(t, err, dispatchErr, "expected original notify error")

	require.NotNil(t, dispatcher.lastEvent, "expected dispatch for failed notification")
	failedEvent, ok := dispatcher.lastEvent.(*events.SendNotificationFailed)
	require.True(t, ok, "expected notification failure event")
	assert.Equal(t, sourceEvent.Type(), failedEvent.EventType, "expected source event type")
	assert.Equal(t, "telegram", failedEvent.Destination, "expected destination kind")
	assert.Equal(t, "ops", failedEvent.Channel, "expected channel name")
	assert.ErrorIs(t, failedEvent.Error, dispatchErr, "expected failure reason")
}

func TestSubscriberHandleDoesNotDispatchFailureForFailureEventItself(t *testing.T) {
	notifier := &testNotifier{
		name: "audit",
		kind: "custom",
		err:  errors.New("webhook down"),
	}
	dispatcher := &testDispatcher{}

	sub := NewSubscriber(notifier, dispatcher)

	err := sub.Handle(context.Background(), &events.SendNotificationFailed{
		EventType:   events.TypeDeployFailed,
		Destination: "custom",
		Channel:     "audit",
		Error:       errors.New("initial"),
	})
	require.Error(t, err, "expected notify error")
	assert.Nil(t, dispatcher.lastEvent, "must not dispatch failure for failure event itself")
}
