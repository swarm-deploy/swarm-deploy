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
	// NodeID is the Docker node identifier.
	NodeID string
	// Action is the normalized Docker event action.
	Action EventAction
	// Attributes contains a copy of the Docker event actor attributes.
	Attributes map[string]string
	// Time is the timestamp reported by Docker.
	Time time.Time
}

// SecretEvent describes a Docker Swarm secret event.
type SecretEvent struct {
	// SecretID is the Docker secret identifier.
	SecretID string
	// Action is the normalized Docker event action.
	Action EventAction
	// Attributes contains a copy of the Docker event actor attributes.
	Attributes map[string]string
	// Time is the timestamp reported by Docker.
	Time time.Time
}

// ResyncReason explains why a subscriber must reconcile its snapshot.
type ResyncReason string

const (
	// ResyncReasonConnected closes the initial snapshot-to-subscription race.
	ResyncReasonConnected ResyncReason = "connected"
	// ResyncReasonReconnect indicates that the Docker event stream reconnected.
	ResyncReasonReconnect ResyncReason = "reconnect"
	// ResyncReasonOverflow indicates that a subscriber's bounded event buffer overflowed.
	ResyncReasonOverflow ResyncReason = "overflow"
)

// ResyncEvent asks a consumer to reconcile its snapshot with Docker.
type ResyncEvent struct {
	// Time is when the resynchronization was requested.
	Time time.Time
	// Reason identifies the condition that may have caused missed events.
	Reason ResyncReason
}

// EventSubscription contains resource events and stream resync notifications.
type EventSubscription[T any] struct {
	// Events contains ordered resource events accepted by the bounded subscriber buffer.
	Events <-chan T
	// Resync contains coalesced snapshot reconciliation requests.
	Resync <-chan ResyncEvent

	unsubscribe func()
}

// Close unregisters the subscription and closes its channels.
func (s EventSubscription[T]) Close() {
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
}

// Events owns the shared Docker events stream and fans out typed events.
type Events struct {
	dockerClient dockerEventsClient

	nodes   eventTopic[NodeEvent]
	secrets eventTopic[SecretEvent]

	reconnectDelay       time.Duration
	subscriberBufferSize int
}

type dockerEventsClient interface {
	// Events opens a filtered Docker events stream.
	Events(context.Context, dockerevents.ListOptions) (<-chan dockerevents.Message, <-chan error)
}

func newEvents(dockerClient dockerEventsClient) *Events {
	return &Events{
		dockerClient:         dockerClient,
		reconnectDelay:       defaultEventReconnectDelay,
		subscriberBufferSize: defaultEventSubscriberBufferSize,
	}
}

// SubscribeNodes subscribes to typed Docker Swarm node events.
func (e *Events) SubscribeNodes() EventSubscription[NodeEvent] {
	return e.nodes.subscribe(e.subscriberBufferSize)
}

// SubscribeSecrets subscribes to typed Docker Swarm secret events.
func (e *Events) SubscribeSecrets() EventSubscription[SecretEvent] {
	return e.secrets.subscribe(e.subscriberBufferSize)
}

// Run consumes one Docker events stream and reconnects it until the context is canceled.
func (e *Events) Run(ctx context.Context) error {
	defer e.close()

	resyncReason := ResyncReasonConnected
	for {
		err := e.watchOnce(ctx, resyncReason)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}

		resyncReason = ResyncReasonReconnect
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

func (e *Events) watchOnce(parent context.Context, resyncReason ResyncReason) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	eventFilters := filters.NewArgs(
		filters.Arg("type", string(dockerevents.NodeEventType)),
		filters.Arg("type", string(dockerevents.SecretEventType)),
	)
	messages, errs := e.dockerClient.Events(ctx, dockerevents.ListOptions{Filters: eventFilters})

	if ctx.Err() != nil {
		return nil
	}
	e.notifyResync(ResyncEvent{Time: time.Now(), Reason: resyncReason})

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-messages:
			if !ok {
				return errors.New("docker events channel closed")
			}
			e.dispatch(ctx, event)
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

func (e *Events) dispatch(ctx context.Context, event dockerevents.Message) {
	overflow := ResyncEvent{Time: time.Now(), Reason: ResyncReasonOverflow}
	var droppedSubscribers int

	//nolint:exhaustive // the Docker stream is filtered to the resource types handled below.
	switch event.Type {
	case dockerevents.NodeEventType:
		droppedSubscribers = e.nodes.publish(NodeEvent{
			NodeID:     event.Actor.ID,
			Action:     EventAction(event.Action),
			Attributes: cloneEventAttributes(event.Actor.Attributes),
			Time:       dockerEventTime(event),
		}, overflow)
	case dockerevents.SecretEventType:
		droppedSubscribers = e.secrets.publish(SecretEvent{
			SecretID:   event.Actor.ID,
			Action:     EventAction(event.Action),
			Attributes: cloneEventAttributes(event.Actor.Attributes),
			Time:       dockerEventTime(event),
		}, overflow)
	default:
		return
	}

	if droppedSubscribers > 0 {
		slog.WarnContext(ctx, "[swarm-events] subscriber buffer overflow; snapshot resync requested",
			slog.String("resource_type", string(event.Type)),
			slog.Int("subscribers", droppedSubscribers),
		)
	}
}

func (e *Events) notifyResync(event ResyncEvent) {
	e.nodes.resync(event)
	e.secrets.resync(event)
}

func (e *Events) close() {
	e.nodes.close()
	e.secrets.close()
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
	nextID      uint64
	subscribers map[uint64]*eventSubscriber[T]
	closed      bool
}

type eventSubscriber[T any] struct {
	events chan T
	resync chan ResyncEvent
}

func (t *eventTopic[T]) subscribe(bufferSize int) EventSubscription[T] {
	t.mu.Lock()
	defer t.mu.Unlock()

	subscriber := &eventSubscriber[T]{
		events: make(chan T, bufferSize),
		resync: make(chan ResyncEvent, 1),
	}
	if t.closed {
		close(subscriber.events)
		close(subscriber.resync)
		return EventSubscription[T]{Events: subscriber.events, Resync: subscriber.resync}
	}
	if t.subscribers == nil {
		t.subscribers = map[uint64]*eventSubscriber[T]{}
	}

	id := t.nextID
	t.nextID++
	t.subscribers[id] = subscriber

	return EventSubscription[T]{
		Events:      subscriber.events,
		Resync:      subscriber.resync,
		unsubscribe: func() { t.unsubscribe(id) },
	}
}

func (t *eventTopic[T]) unsubscribe(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	subscriber, exists := t.subscribers[id]
	if !exists {
		return
	}
	delete(t.subscribers, id)
	close(subscriber.events)
	close(subscriber.resync)
}

func (t *eventTopic[T]) publish(event T, overflow ResyncEvent) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	droppedSubscribers := 0
	for _, subscriber := range t.subscribers {
		select {
		case subscriber.events <- event:
		default:
			if subscriber.notifyResync(overflow) {
				droppedSubscribers++
			}
		}
	}

	return droppedSubscribers
}

func (t *eventTopic[T]) resync(event ResyncEvent) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, subscriber := range t.subscribers {
		subscriber.notifyResync(event)
	}
}

func (t *eventTopic[T]) close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}
	t.closed = true
	for id, subscriber := range t.subscribers {
		delete(t.subscribers, id)
		close(subscriber.events)
		close(subscriber.resync)
	}
}

func (s *eventSubscriber[T]) notifyResync(event ResyncEvent) bool {
	select {
	case s.resync <- event:
		return true
	default:
		return false
	}
}
