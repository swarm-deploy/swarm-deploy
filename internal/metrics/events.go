package metrics

import (
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/prometheus/client_golang/prometheus"
)

type Events interface {
	IncTotal(typ events.Type)

	collectors() []prometheus.Collector
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
	e.total.WithLabelValues(string(typ)).Inc()
}

func (e *prometheusEvents) collectors() []prometheus.Collector {
	return []prometheus.Collector{e.total}
}
