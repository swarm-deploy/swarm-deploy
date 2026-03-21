package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/artarts36/swarm-deploy/internal/event/events"
)

const (
	defaultEventsQueueLen         = 128
	defaultSubscribeHandleTimeout = 5 * time.Minute
)

type QueueDispatcher struct {
	subscribers map[events.Type][]Subscriber

	now   func() time.Time
	queue chan Event

	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
}

func NewQueueDispatcher(subscribers map[events.Type][]Subscriber) *QueueDispatcher {
	d := &QueueDispatcher{
		now:         time.Now,
		queue:       make(chan Event, defaultEventsQueueLen),
		subscribers: subscribers,
	}

	d.wg.Add(1)
	go d.runWorker()

	return d
}

func (d *QueueDispatcher) Dispatch(ctx context.Context, event Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		slog.InfoContext(ctx, "[event] event not dispatched, channel closed", slog.Any("event", event))

		return
	}

	slog.InfoContext(ctx, "[event] dispatching event", slog.Any("event", event))

	d.queue <- event
}

func (d *QueueDispatcher) runWorker() {
	defer d.wg.Done()

	handle := func(subscriber Subscriber, event Event) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultSubscribeHandleTimeout)
		defer cancel()

		err := subscriber.Handle(context.Background(), event)
		if err != nil {
			slog.WarnContext(ctx, "[event] subscriber failed", slog.Any("err", err),
				slog.String("event.type", string(event.Type())),
			)
		}
	}

	for event := range d.queue {
		for _, subscriber := range d.subscribers[event.Type()] {
			handle(subscriber, event)
		}
	}
}

func (d *QueueDispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errors.New("dispatcher already shut down")
	}
	d.closed = true
	close(d.queue)
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
