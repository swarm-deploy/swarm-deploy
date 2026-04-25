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
)

const (
	defaultEventsQueueLen         = 128
	defaultSubscribeHandleTimeout = 5 * time.Minute
)

type QueueDispatcher struct {
	subscribers map[events.Type][]Subscriber

	now func() time.Time

	queue     chan events.Event
	fastQueue *queue
	slowQueue *queue
	handled   map[string]time.Time

	mu       sync.RWMutex
	handledM sync.Mutex
	closed   bool
	wg       sync.WaitGroup
}

const workersCount = 1

func NewQueueDispatcher() *QueueDispatcher {
	d := &QueueDispatcher{
		now:         time.Now,
		queue:       make(chan events.Event, defaultEventsQueueLen),
		subscribers: map[events.Type][]Subscriber{},
		fastQueue:   newQueue(),
		slowQueue:   newQueue(),
		handled:     map[string]time.Time{},
	}

	d.wg.Add(workersCount)
	go d.runQueueWorker()

	return d
}

func (d *QueueDispatcher) Dispatch(ctx context.Context, event events.Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		slog.InfoContext(ctx, "[event] event not dispatched, channel closed", slog.Any("event", event))

		return
	}

	slog.InfoContext(ctx, "[event] dispatching event", slog.Any("event", event),
		slog.String("event.type", event.Type().String()),
	)

	d.queue <- event
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

	for event := range d.queue {
		now := d.now()
		if d.skipDispatching(now, event) {
			slog.DebugContext(context.Background(), "[event] event skipped by deduplication window",
				slog.String("event.type", event.Type().String()),
			)

			continue
		}

		d.mu.RLock()
		subscribers := append([]Subscriber{}, d.subscribers[event.Type()]...)
		d.mu.RUnlock()

		for _, subscriber := range subscribers {
			targetQueue := d.fastQueue

			if subscriber.Slow() {
				targetQueue = d.slowQueue
			}

			targetQueue.Dispatch(&queueTask{
				Event:      event,
				Subscriber: subscriber,
			})
		}
	}
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
