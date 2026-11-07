package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Sync interface {
	RecordSyncRun(reason, result string, duration time.Duration)
	// RecordStackOperation records one stack operation executed by the stack queue.
	RecordStackOperation(stack, operation, result string, duration time.Duration)
	// SetStackQueue records the number of stacks waiting for and running an operation.
	SetStackQueue(pending, running int)
	collectors() []prometheus.Collector
}

type prometheusSync struct {
	runs              *prometheus.CounterVec
	duration          *prometheus.HistogramVec
	stackOperations   *prometheus.CounterVec
	stackDuration     *prometheus.HistogramVec
	stackQueuePending prometheus.Gauge
	stackQueueRunning prometheus.Gauge
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
		stackOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "sync",
				Name:      "stack_operations_total",
				Help:      "Number of stack operations grouped by stack, operation and result.",
			},
			[]string{"stack", "operation", "result"},
		),
		stackDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "sync",
				Name:      "stack_operation_duration_seconds",
				Help:      "Stack operation duration in seconds.",
				Buckets:   []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1200},
			},
			[]string{"operation", "result"},
		),
		stackQueuePending: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "sync",
			Name:      "stack_queue_pending",
			Help:      "Number of stacks with an operation waiting to start.",
		}),
		stackQueueRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "sync",
			Name:      "stack_queue_running",
			Help:      "Number of stacks currently being reconciled or cleaned up.",
		}),
	}
}

func (s *prometheusSync) RecordSyncRun(reason, result string, duration time.Duration) {
	s.runs.WithLabelValues(reason, result).Inc()
	s.duration.WithLabelValues(reason, result).Observe(duration.Seconds())
}

func (s *prometheusSync) RecordStackOperation(stack, operation, result string, duration time.Duration) {
	s.stackOperations.WithLabelValues(stack, operation, result).Inc()
	s.stackDuration.WithLabelValues(operation, result).Observe(duration.Seconds())
}

func (s *prometheusSync) SetStackQueue(pending, running int) {
	s.stackQueuePending.Set(float64(pending))
	s.stackQueueRunning.Set(float64(running))
}

func (s *prometheusSync) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		s.runs, s.duration, s.stackOperations, s.stackDuration, s.stackQueuePending, s.stackQueueRunning,
	}
}
