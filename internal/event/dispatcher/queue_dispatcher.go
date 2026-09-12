package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultEventsQueueLen         = 128
	defaultSubscribeHandleTimeout = 5 * time.Minute
)

type QueueDispatcher struct {
	subscribers map[events.Type][]Subscriber

	now func() time.Time

	queue     chan scheduledMessage
	fastQueue *queue
	slowQueue *queue
	handled   map[string]time.Time

	mu       sync.RWMutex
	handledM sync.Mutex
	closed   bool
	wg       sync.WaitGroup

	tracer trace.Tracer
}

const workersCount = 1

func NewQueueDispatcher() *QueueDispatcher {
	d := &QueueDispatcher{
		now:         time.Now,
		queue:       make(chan scheduledMessage, defaultEventsQueueLen),
		subscribers: map[events.Type][]Subscriber{},
		fastQueue:   newQueue("fast"),
		slowQueue:   newQueue("slow"),
		handled:     map[string]time.Time{},
		tracer:      otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"),
	}

	d.wg.Add(workersCount)
	go d.runQueueWorker()

	return d
}

func (d *QueueDispatcher) Dispatch(ctx context.Context, event events.Event) {
	ctx, span := d.tracer.Start(ctx, "event.Dispatch", trace.WithAttributes(
		tracing.EventName.String(string(event.Type().Name())),
	))
	defer span.End()

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		slog.InfoContext(ctx, "[event] event not dispatched, channel closed", slog.Any("event", event))

		tracing.FailSpan(span, errors.New("event dispatcher is closed"))

		return
	}

	slog.InfoContext(ctx, "[event] dispatching event", slog.Any("event", event),
		slog.String("event.type", event.Type().String()),
	)

	d.queue <- scheduledMessage{
		Event:       event,
		SpanContext: trace.SpanContextFromContext(ctx),
	}

	span.AddEvent("Event scheduled")
}

func (d *QueueDispatcher) skipDispatching(now time.Time, event events.Event) bool {
	window := event.Type().Window()
	if window <= 0 {
		return false
	}

	key := deduplicateKey(event)

	d.handledM.Lock()
	defer d.handledM.Unlock()
	d.cleanHandledLocked(now)

	if nextHandle, ok := d.handled[key]; ok && now.Before(nextHandle) {
		return true
	}

	d.handled[key] = now.Add(window)
	return false
}

// Subscribe registers a subscriber for event type.
func (d *QueueDispatcher) Subscribe(eventType events.Type, subscriber Subscriber) {
	d.mu.Lock()
	d.subscribers[eventType] = append(d.subscribers[eventType], subscriber)
	d.mu.Unlock()
}

func (d *QueueDispatcher) runQueueWorker() {
	defer d.wg.Done()

	process := func(msg scheduledMessage) {
		ctx, span := d.tracer.Start(
			trace.ContextWithSpanContext(context.Background(), msg.SpanContext),
			"event.Process",
			trace.WithAttributes(
				tracing.EventName.String(msg.Event.Type().String()),
			),
		)
		defer span.End()

		now := d.now()
		if d.skipDispatching(now, msg.Event) {
			slog.DebugContext(context.Background(), "[event] event skipped by deduplication window",
				slog.String("event.type", msg.Event.Type().String()),
			)

			span.AddEvent("event skipped by deduplication window")

			return
		}

		d.mu.RLock()
		subscribers := append([]Subscriber{}, d.subscribers[msg.Event.Type()]...)
		d.mu.RUnlock()

		for _, subscriber := range subscribers {
			d.forwardToQueue(ctx, msg, subscriber)
		}
	}

	for event := range d.queue {
		process(event)
	}
}

func (d *QueueDispatcher) forwardToQueue(ctx context.Context, msg scheduledMessage, subscriber Subscriber) {
	ctx, span := d.tracer.Start(ctx, "event.ForwardToQueue", trace.WithAttributes(
		tracing.EventName.String(string(msg.Event.Type().Name())),
	))
	defer span.End()

	targetQueue := d.fastQueue
	if subscriber.Slow() {
		targetQueue = d.slowQueue
	}

	span.SetAttributes(tracing.EventQueueName.String(targetQueue.Name()))

	targetQueue.Dispatch(&message{
		Event:       msg.Event,
		Subscriber:  subscriber,
		SpanContext: trace.SpanContextFromContext(ctx),
	})

	span.AddEvent("Event forwarded")
}

func (d *QueueDispatcher) cleanHandledLocked(now time.Time) {
	for key, nextHandle := range d.handled {
		if now.Before(nextHandle) {
			continue
		}

		delete(d.handled, key)
	}
}

func deduplicateKey(event events.Event) string {
	details := event.Details()
	keys := make([]string, 0, len(details))

	for key := range details {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	hash := fnv.New64a()
	_, _ = hash.Write([]byte(event.Type().String()))
	_, _ = hash.Write([]byte{0})

	for _, key := range keys {
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(details[key]))
		_, _ = hash.Write([]byte{0})
	}

	return fmt.Sprintf("%s:%x", event.Type().String(), hash.Sum64())
}

func (d *QueueDispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errors.New("dispatcher already shut down")
	}
	d.closed = true
	close(d.queue)

	d.slowQueue.Close()
	d.fastQueue.Close()

	d.mu.Unlock()

	waitDone := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		return nil
	case <-ctx.Done():
		return errors.Join(errors.New("shutdown dispatcher"), ctx.Err())
	}
}
