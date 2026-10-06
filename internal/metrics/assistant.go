package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Assistant interface {
	subsystem

	// RecordChatCreated tracks newly created assistant chats (conversation threads).
	RecordChatCreated()
	// RecordIndexRebuild tracks index rebuild outcome and timing.
	RecordIndexRebuild(status string, size int, duration time.Duration, updatedAt time.Time)
	// RecordRetrieveFallback tracks fallback reasons during retrieval.
	RecordRetrieveFallback(reason string)
	// RecordRouterFallback tracks reasons that assistant routing used the fallback profile.
	RecordRouterFallback(reason string)
}

type prometheusAssistant struct {
	chatCreatedTotal         prometheus.Counter
	ragIndexRebuildTotal     *prometheus.CounterVec
	ragIndexRebuildDuration  *prometheus.HistogramVec
	ragRetrieveFallbackTotal *prometheus.CounterVec
	routerFallbackTotal      *prometheus.CounterVec
	ragIndexSize             prometheus.Gauge
	ragIndexUpdatedAt        prometheus.Gauge
}

type NopAssistant struct{}

func (NopAssistant) collectors() []prometheus.Collector {
	return []prometheus.Collector{}
}

func (NopAssistant) RecordChatCreated() {}

func (NopAssistant) RecordIndexRebuild(string, int, time.Duration, time.Time) {}

func (NopAssistant) RecordRetrieveFallback(string) {}

func (NopAssistant) RecordRouterFallback(string) {}

func newAssistant(namespace string, enabled bool) Assistant {
	if enabled {
		return newPrometheusAssistant(namespace)
	}
	return &NopAssistant{}
}

func newPrometheusAssistant(namespace string) Assistant {
	return &prometheusAssistant{
		chatCreatedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "assistant",
				Name:      "chats_total",
				Help:      "Number of newly created assistant chats (conversation threads).",
			},
		),
		ragIndexRebuildTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "assistant",
				Name:      "rag_index_rebuild_total",
				Help:      "Number of RAG index rebuild attempts grouped by status.",
			},
			[]string{"status"},
		),
		ragIndexRebuildDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "assistant",
				Name:      "rag_index_rebuild_duration_seconds",
				Help:      "RAG index rebuild duration in seconds grouped by status.",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
			},
			[]string{"status"},
		),
		ragRetrieveFallbackTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "assistant",
				Name:      "rag_retrieve_fallback_total",
				Help:      "Number of retrieval fallbacks grouped by reason.",
			},
			[]string{"reason"},
		),
		routerFallbackTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "assistant",
				Name:      "router_fallback_total",
				Help:      "Number of assistant router fallbacks grouped by reason.",
			},
			[]string{"reason"},
		),
		ragIndexSize: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "assistant",
				Name:      "rag_index_size",
				Help:      "Current number of services in RAG index snapshot.",
			},
		),
		ragIndexUpdatedAt: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "assistant",
				Name:      "rag_index_updated_at_unix",
				Help:      "Unix timestamp of the last successful RAG index update.",
			},
		),
	}
}

func (m *prometheusAssistant) RecordChatCreated() {
	m.chatCreatedTotal.Inc()
}

func (m *prometheusAssistant) RecordIndexRebuild(status string, size int, duration time.Duration, updatedAt time.Time) {
	m.ragIndexRebuildTotal.WithLabelValues(status).Inc()
	m.ragIndexRebuildDuration.WithLabelValues(status).Observe(duration.Seconds())
	m.ragIndexSize.Set(float64(size))
	if !updatedAt.IsZero() {
		m.ragIndexUpdatedAt.Set(float64(updatedAt.Unix()))
	}
}

func (m *prometheusAssistant) RecordRetrieveFallback(reason string) {
	m.ragRetrieveFallbackTotal.WithLabelValues(reason).Inc()
}

func (m *prometheusAssistant) RecordRouterFallback(reason string) {
	m.routerFallbackTotal.WithLabelValues(reason).Inc()
}

func (m *prometheusAssistant) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.chatCreatedTotal,
		m.ragIndexRebuildTotal,
		m.ragIndexRebuildDuration,
		m.ragRetrieveFallbackTotal,
		m.routerFallbackTotal,
		m.ragIndexSize,
		m.ragIndexUpdatedAt,
	}
}
