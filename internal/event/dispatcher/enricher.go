package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type Enricher func(ctx context.Context, event events.Event) events.Event

type EnrichableDispatcher struct {
	propagator Enricher
	dispatcher Dispatcher
}

type composeEnricher struct {
	propagators []Enricher
}

func WrapEnrichers(propagators ...Enricher) Enricher {
	p := composeEnricher{propagators: propagators}

	return p.enrich
}

func NewEnrichableDispatcher(propagator Enricher, dispatcher Dispatcher) *EnrichableDispatcher {
	return &EnrichableDispatcher{
		propagator: propagator,
		dispatcher: dispatcher,
	}
}

func (d *EnrichableDispatcher) Dispatch(ctx context.Context, event events.Event) {
	d.dispatcher.Dispatch(ctx, d.propagator(ctx, event))
}

func (d *EnrichableDispatcher) Subscribe(eventType events.Type, subscriber Subscriber) {
	d.dispatcher.Subscribe(eventType, subscriber)
}

func (d *EnrichableDispatcher) Shutdown(ctx context.Context) error {
	return d.dispatcher.Shutdown(ctx)
}

func (p *composeEnricher) enrich(ctx context.Context, event events.Event) events.Event {
	for _, propagator := range p.propagators {
		event = propagator(ctx, event)
	}

	return event
}
