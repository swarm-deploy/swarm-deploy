package swarm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventsRoutesTypedEventsThroughSingleDockerStream(t *testing.T) {
	t.Parallel()

	client := newFakeDockerEventsClient()
	events := newEvents(client)
	nodes := events.SubscribeNodes()
	secrets := events.SubscribeSecrets()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- events.Run(ctx)
	}()

	options := requireEventOptions(t, client.options)
	assert.ElementsMatch(t,
		[]string{string(dockerevents.NodeEventType), string(dockerevents.SecretEventType)},
		options.Filters.Get("type"),
	)
	assert.Equal(t, ResyncReasonConnected, requireEvent(t, nodes.Resync).Reason)
	assert.Equal(t, ResyncReasonConnected, requireEvent(t, secrets.Resync).Reason)

	client.messages <- dockerevents.Message{
		Type:   dockerevents.NodeEventType,
		Action: "update",
		Actor: dockerevents.Actor{
			ID:         "node-1",
			Attributes: map[string]string{"name": "worker-1"},
		},
		Time: 10,
	}

	nodeEvent := requireEvent(t, nodes.Events)
	assert.Equal(t, "node-1", nodeEvent.NodeID)
	assert.Equal(t, EventActionUpdate, nodeEvent.Action)
	assert.Equal(t, map[string]string{"name": "worker-1"}, nodeEvent.Attributes)
	assert.Equal(t, time.Unix(10, 0), nodeEvent.Time)

	client.messages <- dockerevents.Message{
		Type:   dockerevents.SecretEventType,
		Action: "create",
		Actor: dockerevents.Actor{
			ID:         "secret-1",
			Attributes: map[string]string{"name": "database-password"},
		},
		TimeNano: 42,
	}

	secretEvent := requireEvent(t, secrets.Events)
	assert.Equal(t, "secret-1", secretEvent.SecretID)
	assert.Equal(t, EventActionCreate, secretEvent.Action)
	assert.Equal(t, map[string]string{"name": "database-password"}, secretEvent.Attributes)
	assert.Equal(t, time.Unix(0, 42), secretEvent.Time)

	cancel()
	require.NoError(t, <-done)
}

func TestEventsPublishesResyncToAllSubscribersAfterReconnect(t *testing.T) {
	t.Parallel()

	client := newFakeDockerEventsClient()
	events := newEvents(client)
	events.reconnectDelay = time.Millisecond
	nodesOne := events.SubscribeNodes()
	nodesTwo := events.SubscribeNodes()
	secrets := events.SubscribeSecrets()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- events.Run(ctx)
	}()

	requireEventOptions(t, client.options)
	firstStreamCtx := requireEvent(t, client.contexts)
	assert.Equal(t, ResyncReasonConnected, requireEvent(t, nodesOne.Resync).Reason)
	assert.Equal(t, ResyncReasonConnected, requireEvent(t, nodesTwo.Resync).Reason)
	assert.Equal(t, ResyncReasonConnected, requireEvent(t, secrets.Resync).Reason)

	client.errs <- errors.New("connection lost")
	requireEventOptions(t, client.options)
	requireEvent(t, client.contexts)
	require.Eventually(t, func() bool { return firstStreamCtx.Err() != nil }, time.Second, time.Millisecond)

	assert.Equal(t, ResyncReasonReconnect, requireEvent(t, nodesOne.Resync).Reason)
	assert.Equal(t, ResyncReasonReconnect, requireEvent(t, nodesTwo.Resync).Reason)
	assert.Equal(t, ResyncReasonReconnect, requireEvent(t, secrets.Resync).Reason)

	cancel()
	require.NoError(t, <-done)
}

func TestEventsReconnectsWhenDockerStreamChannelCloses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		close func(messages chan dockerevents.Message, errs chan error)
	}{
		{
			name: "messages channel",
			close: func(messages chan dockerevents.Message, _ chan error) {
				close(messages)
			},
		},
		{
			name: "errors channel",
			close: func(_ chan dockerevents.Message, errs chan error) {
				close(errs)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeDockerEventsClient()
			events := newEvents(client)
			events.reconnectDelay = time.Millisecond
			subscription := events.SubscribeNodes()

			messages := make(chan dockerevents.Message)
			errs := make(chan error)
			client.streams <- dockerEventChannels{messages: messages, errs: errs}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- events.Run(ctx) }()

			requireEventOptions(t, client.options)
			requireEvent(t, client.contexts)
			assert.Equal(t, ResyncReasonConnected, requireEvent(t, subscription.Resync).Reason)
			test.close(messages, errs)
			requireEventOptions(t, client.options)
			requireEvent(t, client.contexts)
			assert.Equal(t, ResyncReasonReconnect, requireEvent(t, subscription.Resync).Reason)

			cancel()
			require.NoError(t, <-done)
		})
	}
}

func TestEventsSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	t.Parallel()

	client := newFakeDockerEventsClient()
	events := newEvents(client)
	events.subscriberBufferSize = 1
	slow := events.SubscribeNodes()
	active := events.SubscribeNodes()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- events.Run(ctx)
	}()

	requireEventOptions(t, client.options)
	requireEvent(t, slow.Resync)
	requireEvent(t, active.Resync)

	client.messages <- dockerNodeEvent("node-1")
	assert.Equal(t, "node-1", requireEvent(t, active.Events).NodeID)

	client.messages <- dockerNodeEvent("node-2")
	assert.Equal(t, "node-2", requireEvent(t, active.Events).NodeID)
	assert.Equal(t, ResyncReasonOverflow, requireEvent(t, slow.Resync).Reason)

	cancel()
	require.NoError(t, <-done)
}

func TestEventOverflowIsBoundedAndRequestsOneResync(t *testing.T) {
	t.Parallel()

	events := newEvents(nil)
	events.subscriberBufferSize = 1
	subscription := events.SubscribeNodes()
	defer subscription.Close()

	events.dispatch(context.Background(), dockerNodeEvent("node-1"))
	events.dispatch(context.Background(), dockerNodeEvent("node-2"))
	events.dispatch(context.Background(), dockerNodeEvent("node-3"))

	assert.Equal(t, "node-1", requireEvent(t, subscription.Events).NodeID)
	assert.Equal(t, ResyncReasonOverflow, requireEvent(t, subscription.Resync).Reason)
	assertNoEvent(t, subscription.Events)
	assertNoEvent(t, subscription.Resync)
}

func TestEventTopicPreservesAcceptedEventOrder(t *testing.T) {
	t.Parallel()

	events := newEvents(nil)
	events.subscriberBufferSize = 3
	subscription := events.SubscribeNodes()
	defer subscription.Close()

	for _, id := range []string{"node-1", "node-2", "node-3"} {
		events.dispatch(context.Background(), dockerNodeEvent(id))
	}

	assert.Equal(t, "node-1", requireEvent(t, subscription.Events).NodeID)
	assert.Equal(t, "node-2", requireEvent(t, subscription.Events).NodeID)
	assert.Equal(t, "node-3", requireEvent(t, subscription.Events).NodeID)
}

func TestEventTopicSupportsConcurrentSubscribersAndClose(t *testing.T) {
	t.Parallel()

	events := newEvents(nil)
	const subscriberCount = 32

	subscriptions := make([]EventSubscription[NodeEvent], subscriberCount)
	var waitGroup sync.WaitGroup
	for index := range subscriptions {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			subscriptions[index] = events.SubscribeNodes()
		}()
	}
	waitGroup.Wait()

	events.dispatch(context.Background(), dockerNodeEvent("node-1"))
	for _, subscription := range subscriptions {
		assert.Equal(t, "node-1", requireEvent(t, subscription.Events).NodeID)
		subscription.Close()
		subscription.Close()
		requireClosed(t, subscription.Events)
		requireClosed(t, subscription.Resync)
	}
}

func TestEventTopicConcurrentDispatchAndClose(t *testing.T) {
	t.Parallel()

	events := newEvents(nil)
	subscription := events.SubscribeNodes()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 1_000; index++ {
			events.dispatch(context.Background(), dockerNodeEvent("node-1"))
		}
	}()
	subscription.Close()
	<-done

	requireClosed(t, subscription.Events)
	requireClosed(t, subscription.Resync)
}

func TestEventsShutdownCancelsStreamAndClosesSubscriptions(t *testing.T) {
	t.Parallel()

	client := newFakeDockerEventsClient()
	events := newEvents(client)
	subscription := events.SubscribeNodes()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- events.Run(ctx)
	}()

	requireEventOptions(t, client.options)
	streamCtx := requireEvent(t, client.contexts)
	requireEvent(t, subscription.Resync)
	cancel()
	require.NoError(t, <-done)
	require.Error(t, streamCtx.Err())
	requireClosed(t, subscription.Events)
	requireClosed(t, subscription.Resync)

	late := events.SubscribeNodes()
	requireClosed(t, late.Events)
	requireClosed(t, late.Resync)
}

func dockerNodeEvent(nodeID string) dockerevents.Message {
	return dockerevents.Message{
		Type:   dockerevents.NodeEventType,
		Action: dockerevents.ActionUpdate,
		Actor:  dockerevents.Actor{ID: nodeID},
	}
}

type fakeDockerEventsClient struct {
	messages chan dockerevents.Message
	errs     chan error
	options  chan dockerevents.ListOptions
	contexts chan context.Context
	streams  chan dockerEventChannels
}

type dockerEventChannels struct {
	messages <-chan dockerevents.Message
	errs     <-chan error
}

func newFakeDockerEventsClient() *fakeDockerEventsClient {
	return &fakeDockerEventsClient{
		messages: make(chan dockerevents.Message, 64),
		errs:     make(chan error, 8),
		options:  make(chan dockerevents.ListOptions, 8),
		contexts: make(chan context.Context, 8),
		streams:  make(chan dockerEventChannels, 8),
	}
}

func (c *fakeDockerEventsClient) Events(
	ctx context.Context,
	options dockerevents.ListOptions,
) (<-chan dockerevents.Message, <-chan error) {
	c.options <- options
	c.contexts <- ctx
	select {
	case stream := <-c.streams:
		return stream.messages, stream.errs
	default:
	}
	return c.messages, c.errs
}

func requireEventOptions(t *testing.T, options <-chan dockerevents.ListOptions) dockerevents.ListOptions {
	t.Helper()

	select {
	case value := <-options:
		return value
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for Docker events subscription")
		return dockerevents.ListOptions{}
	}
}

func requireEvent[T any](t *testing.T, events <-chan T) T {
	t.Helper()

	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for event")
		var zero T
		return zero
	}
}

func requireClosed[T any](t *testing.T, events <-chan T) {
	t.Helper()

	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timeout.C:
			require.FailNow(t, "timed out waiting for channel close")
		}
	}
}

func assertNoEvent[T any](t *testing.T, events <-chan T) {
	t.Helper()

	select {
	case <-events:
		assert.Fail(t, "unexpected event")
	case <-time.After(20 * time.Millisecond):
	}
}
