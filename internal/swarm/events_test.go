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

func TestEventsPublishesResyncAfterReconnect(t *testing.T) {
	t.Parallel()

	client := newFakeDockerEventsClient()
	events := newEvents(client)
	events.reconnectDelay = time.Millisecond
	nodes := events.SubscribeNodes()
	secrets := events.SubscribeSecrets()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- events.Run(ctx)
	}()

	requireEventOptions(t, client.options)
	client.errs <- errors.New("connection lost")
	requireEventOptions(t, client.options)

	requireEvent(t, nodes.Resync)
	requireEvent(t, secrets.Resync)

	cancel()
	require.NoError(t, <-done)
}

type fakeDockerEventsClient struct {
	mu       sync.Mutex
	messages chan dockerevents.Message
	errs     chan error
	options  chan dockerevents.ListOptions
}

func newFakeDockerEventsClient() *fakeDockerEventsClient {
	return &fakeDockerEventsClient{
		messages: make(chan dockerevents.Message, 4),
		errs:     make(chan error, 4),
		options:  make(chan dockerevents.ListOptions, 4),
	}
}

func (c *fakeDockerEventsClient) Events(
	_ context.Context,
	options dockerevents.ListOptions,
) (<-chan dockerevents.Message, <-chan error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.options <- options
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
