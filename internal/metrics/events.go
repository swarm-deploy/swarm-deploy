package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type Events interface {
	subsystem

	IncTotal(typ events.Type)
}

type prometheusEvents struct {
	total *prometheus.CounterVec
}

func newPrometheusEvents(namespace string) *prometheusEvents {
	return &prometheusEvents{
		total: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "events",
				Name:      "total",
				Help:      "Number of dispatched total grouped by event type.",
			},
			[]string{"type"},
		),
	}
}

func (e *prometheusEvents) IncTotal(typ events.Type) {
	e.total.WithLabelValues(typ.String()).Inc()
}

func (e *prometheusEvents) collectors() []prometheus.Collector {
	return []prometheus.Collector{e.total}
}
