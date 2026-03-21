package event

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/notify"
)

const (
	defaultEventsQueueLen = 128
)

type Event interface {
	Type() events.Type
}

type notifier interface {
	// Notify sends a notification event.
	Notify(ctx context.Context, event notify.Event) error
}

type Dispatcher struct {
	notifier notifier
	now      func() time.Time
	queue    chan notify.Event

	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
}

func NewDispatcher(notifier notifier) *Dispatcher {
	d := &Dispatcher{
		notifier: notifier,
		now:      time.Now,
		queue:    make(chan notify.Event, defaultEventsQueueLen),
	}

	d.wg.Add(1)
	go d.runWorker()

	return d
}

func (d *Dispatcher) Dispatch(event Event) {
	switch e := event.(type) {
	case *events.DeploySuccess:
		for _, service := range e.Services {
			imageName := service.Image
			if imageName == "" {
				imageName = "unknown"
			}

			d.dispatch(notify.Event{
				Status:    "success",
				StackName: e.StackName,
				Service:   service.Name,
				Image: notify.Image{
					FullName: imageName,
					Version:  compose.ImageVersion(imageName),
				},
				Commit:    e.Commit,
				Timestamp: d.now(),
			})
		}
	case *events.DeployFailed:
		for _, service := range e.Services {
			d.dispatch(notify.Event{
				Status:    "failed",
				StackName: e.StackName,
				Service:   service.Name,
				Image: notify.Image{
					FullName: "unknown",
					Version:  "unknown",
				},
				Commit:    e.Commit,
				Error:     e.Error.Error(),
				Timestamp: d.now(),
			})
		}
	}
}

func (d *Dispatcher) dispatch(event notify.Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return
	}

	d.queue <- event
}

func (d *Dispatcher) runWorker() {
	defer d.wg.Done()

	for event := range d.queue {
		d.notify(event)
	}
}

func (d *Dispatcher) Shutdown(ctx context.Context) error {
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

func (d *Dispatcher) notify(event notify.Event) {
	ctx := context.Background()
	if err := d.notifier.Notify(ctx, event); err != nil {
		slog.ErrorContext(ctx, "[event] failed to notify", slog.Any("err", err))
	}
}
