package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Sync interface {
	RecordSyncRun(reason, result string, duration time.Duration)
	collectors() []prometheus.Collector
}

type prometheusSync struct {
	runs     *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newPrometheusSync(namespace string) *prometheusSync {
	return &prometheusSync{
		runs: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "sync",
				Name:      "runs_total",
				Help:      "Number of sync runs grouped by reason and result.",
			},
			[]string{"reason", "result"},
		),
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "sync_duration_seconds",
				Help:      "Sync run duration in seconds.",
				Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
			},
			[]string{"reason", "result"},
		),
	}
}

func (s *prometheusSync) RecordSyncRun(reason, result string, duration time.Duration) {
	s.runs.WithLabelValues(reason, result).Inc()
	s.duration.WithLabelValues(reason, result).Observe(duration.Seconds())
}

func (s *prometheusSync) collectors() []prometheus.Collector {
	return []prometheus.Collector{s.runs, s.duration}
}
