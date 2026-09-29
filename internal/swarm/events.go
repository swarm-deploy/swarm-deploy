package swarm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

const defaultEventSubscriberBufferSize = 64
const defaultEventReconnectDelay = 5 * time.Second

// EventAction is a normalized Docker resource event action.
type EventAction string

const (
	EventActionCreate EventAction = "create"
	EventActionUpdate EventAction = "update"
	EventActionRemove EventAction = "remove"
)

// NodeEvent describes a Docker Swarm node event.
type NodeEvent struct {
	NodeID     string
	Action     EventAction
	Attributes map[string]string
	Time       time.Time
}

// SecretEvent describes a Docker Swarm secret event.
type SecretEvent struct {
	SecretID   string
	Action     EventAction
	Attributes map[string]string
	Time       time.Time
}

// ResyncEvent asks consumers to refresh their snapshot after the Docker event stream reconnects.
type ResyncEvent struct {
	Time time.Time
}

// EventSubscription contains resource events and stream resync notifications.
type EventSubscription[T any] struct {
	Events <-chan T
	Resync <-chan ResyncEvent
}

// Events owns the shared Docker events stream and fans out typed events.
type Events struct {
	dockerClient dockerEventsClient

	nodes   eventTopic[NodeEvent]
	secrets eventTopic[SecretEvent]
	resync  eventTopic[ResyncEvent]

	reconnectDelay time.Duration
}

type dockerEventsClient interface {
	Events(context.Context, dockerevents.ListOptions) (<-chan dockerevents.Message, <-chan error)
}

func newEvents(dockerClient dockerEventsClient) *Events {
	return &Events{
		dockerClient:   dockerClient,
		reconnectDelay: defaultEventReconnectDelay,
	}
}

// SubscribeNodes subscribes to typed Docker Swarm node events.
func (e *Events) SubscribeNodes() EventSubscription[NodeEvent] {
	return EventSubscription[NodeEvent]{
		Events: e.nodes.subscribe(),
		Resync: e.resync.subscribe(),
	}
}

// SubscribeSecrets subscribes to typed Docker Swarm secret events.
func (e *Events) SubscribeSecrets() EventSubscription[SecretEvent] {
	return EventSubscription[SecretEvent]{
		Events: e.secrets.subscribe(),
		Resync: e.resync.subscribe(),
	}
}

// Run consumes one Docker events stream and reconnects it until the context is canceled.
func (e *Events) Run(ctx context.Context) error {
	reconnected := false
	for {
		err := e.watchOnce(ctx, reconnected)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is a normal shutdown path
		}

		reconnected = true
		slog.WarnContext(ctx, "[swarm-events] docker events stream failed", slog.Any("err", err))

		timer := time.NewTimer(e.reconnectDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (e *Events) watchOnce(ctx context.Context, reconnected bool) error {
	eventFilters := filters.NewArgs(
		filters.Arg("type", string(dockerevents.NodeEventType)),
		filters.Arg("type", string(dockerevents.SecretEventType)),
	)
	messages, errs := e.dockerClient.Events(ctx, dockerevents.ListOptions{Filters: eventFilters})

	if reconnected {
		if err := e.resync.publish(ctx, ResyncEvent{Time: time.Now()}); err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-messages:
			if !ok {
				return errors.New("docker events channel closed")
			}
			if err := e.dispatch(ctx, event); err != nil {
				return err
			}
		case streamErr, ok := <-errs:
			if !ok {
				return errors.New("docker events errors channel closed")
			}
			if streamErr != nil {
				return fmt.Errorf("watch docker events: %w", streamErr)
			}
		}
	}
}

func (e *Events) dispatch(ctx context.Context, event dockerevents.Message) error {
	//nolint:exhaustive // the Docker stream is filtered to the resource types handled below.
	switch event.Type {
	case dockerevents.NodeEventType:
		return e.nodes.publish(ctx, NodeEvent{
			NodeID:     event.Actor.ID,
			Action:     EventAction(event.Action),
			Attributes: cloneEventAttributes(event.Actor.Attributes),
			Time:       dockerEventTime(event),
		})
	case dockerevents.SecretEventType:
		return e.secrets.publish(ctx, SecretEvent{
			SecretID:   event.Actor.ID,
			Action:     EventAction(event.Action),
			Attributes: cloneEventAttributes(event.Actor.Attributes),
			Time:       dockerEventTime(event),
		})
	default:
		return nil
	}
}

func dockerEventTime(event dockerevents.Message) time.Time {
	if event.TimeNano != 0 {
		return time.Unix(0, event.TimeNano)
	}
	if event.Time != 0 {
		return time.Unix(event.Time, 0)
	}
	return time.Time{}
}

func cloneEventAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}

type eventTopic[T any] struct {
	mu          sync.RWMutex
	subscribers []chan T
}

func (t *eventTopic[T]) subscribe() <-chan T {
	subscription := make(chan T, defaultEventSubscriberBufferSize)

	t.mu.Lock()
	t.subscribers = append(t.subscribers, subscription)
	t.mu.Unlock()

	return subscription
}

func (t *eventTopic[T]) publish(ctx context.Context, event T) error {
	t.mu.RLock()
	subscribers := append([]chan T(nil), t.subscribers...)
	t.mu.RUnlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
