package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type Propagator func(ctx context.Context, event events.Event) events.Event

type PropagatableDispatcher struct {
	propagator Propagator
	dispatcher Dispatcher
}

type composePropagator struct {
	propagators []Propagator
}

func WrapPropagators(propagators ...Propagator) Propagator {
	p := composePropagator{propagators: propagators}

	return p.propagate
}

func NewPropagatableDispatcher(propagator Propagator, dispatcher Dispatcher) *PropagatableDispatcher {
	return &PropagatableDispatcher{
		propagator: propagator,
		dispatcher: dispatcher,
	}
}

func (d *PropagatableDispatcher) Dispatch(ctx context.Context, event events.Event) {
	d.dispatcher.Dispatch(ctx, d.propagator(ctx, event))
}

func (d *PropagatableDispatcher) Subscribe(eventType events.Type, subscriber Subscriber) {
	d.dispatcher.Subscribe(eventType, subscriber)
}

func (d *PropagatableDispatcher) Shutdown(ctx context.Context) error {
	return d.dispatcher.Shutdown(ctx)
}

func (p *composePropagator) propagate(ctx context.Context, event events.Event) events.Event {
	for _, propagator := range p.propagators {
		event = propagator(ctx, event)
	}

	return event
}
