package metrics

import (
	"time"

	"github.com/artarts36/swarm-deploy/internal/assistant/rag"
	"github.com/prometheus/client_golang/prometheus"
)

// Recorder stores assistant-specific telemetry metrics.
type Recorder struct {
	ragIndexRebuildTotal     *prometheus.CounterVec
	ragIndexRebuildDuration  *prometheus.HistogramVec
	ragRetrieveFallbackTotal *prometheus.CounterVec
	ragIndexSize             prometheus.Gauge
	ragIndexUpdatedAt        prometheus.Gauge
}

var _ rag.Observer = (*Recorder)(nil)

// New creates assistant metrics recorder and registers all collectors.
func New(namespace string) *Recorder {
	return &Recorder{
		ragIndexRebuildTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "assistant_rag_index_rebuild_total",
				Help:      "Number of RAG index rebuild attempts grouped by status.",
			},
			[]string{"status"},
		),
		ragIndexRebuildDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "assistant_rag_index_rebuild_duration_seconds",
				Help:      "RAG index rebuild duration in seconds grouped by status.",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
			},
			[]string{"status"},
		),
		ragRetrieveFallbackTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "assistant_rag_retrieve_fallback_total",
				Help:      "Number of retrieval fallbacks grouped by reason.",
			},
			[]string{"reason"},
		),
		ragIndexSize: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "assistant_rag_index_size",
				Help:      "Current number of services in RAG index snapshot.",
			},
		),
		ragIndexUpdatedAt: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "assistant_rag_index_updated_at_unix",
				Help:      "Unix timestamp of the last successful RAG index update.",
			},
		),
	}
}

func (r *Recorder) Describe(ch chan<- *prometheus.Desc) {
	r.ragIndexRebuildTotal.Describe(ch)
	r.ragIndexRebuildDuration.Describe(ch)
	r.ragRetrieveFallbackTotal.Describe(ch)
	r.ragIndexSize.Describe(ch)
	r.ragIndexUpdatedAt.Describe(ch)
}

func (r *Recorder) Collect(ch chan<- prometheus.Metric) {
	r.ragIndexRebuildTotal.Collect(ch)
	r.ragIndexRebuildDuration.Collect(ch)
	r.ragRetrieveFallbackTotal.Collect(ch)
	r.ragIndexSize.Collect(ch)
	r.ragIndexUpdatedAt.Collect(ch)
}

// RecordIndexRebuild tracks index rebuild outcome and timing.
func (r *Recorder) RecordIndexRebuild(status string, size int, duration time.Duration, updatedAt time.Time) {
	r.ragIndexRebuildTotal.WithLabelValues(status).Inc()
	r.ragIndexRebuildDuration.WithLabelValues(status).Observe(duration.Seconds())
	r.ragIndexSize.Set(float64(size))
	if !updatedAt.IsZero() {
		r.ragIndexUpdatedAt.Set(float64(updatedAt.Unix()))
	}
}

// RecordRetrieveFallback tracks lexical fallback reasons.
func (r *Recorder) RecordRetrieveFallback(reason string) {
	r.ragRetrieveFallbackTotal.WithLabelValues(reason).Inc()
}
