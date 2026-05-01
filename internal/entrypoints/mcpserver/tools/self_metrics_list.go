package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// SelfMetricsList returns current internal Prometheus metrics snapshot.
type SelfMetricsList struct {
	gatherer     prometheus.Gatherer
	metricPrefix string
}

// NewSelfMetricsList creates self_metrics_list component.
func NewSelfMetricsList(gatherer prometheus.Gatherer, metricPrefix string) *SelfMetricsList {
	return &SelfMetricsList{
		gatherer:     gatherer,
		metricPrefix: metricPrefix,
	}
}

// Definition returns tool metadata visible to the model.
func (l *SelfMetricsList) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "self_metrics_list",
		Description: "Returns current swarm-deploy Prometheus metrics snapshot.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Request: struct{}{},
	}
}

// Execute runs self_metrics_list tool.
func (l *SelfMetricsList) Execute(_ context.Context, _ routing.Request) (routing.Response, error) {
	metricFamilies, err := l.gatherer.Gather()
	if err != nil {
		return routing.Response{}, fmt.Errorf("gather metrics: %w", err)
	}

	payload := selfMetricsListPayload{
		Metrics: make([]selfMetricPayload, 0, len(metricFamilies)),
	}
	for _, metricFamily := range metricFamilies {
		if metricFamily == nil {
			continue
		}
		if !strings.HasPrefix(metricFamily.GetName(), l.metricPrefix) {
			continue
		}

		payload.Metrics = append(payload.Metrics, mapMetricFamilyPayload(metricFamily))
	}

	return routing.Response{
		Payload: payload,
	}, nil
}

func mapMetricFamilyPayload(metricFamily *dto.MetricFamily) selfMetricPayload {
	metricType, metricTypeName := resolveMetricType(metricFamily)

	payload := selfMetricPayload{
		Name:    metricFamily.GetName(),
		Help:    metricFamily.GetHelp(),
		Type:    metricTypeName,
		Samples: make([]selfMetricSamplePayload, 0, len(metricFamily.GetMetric())),
	}
	for _, metric := range metricFamily.GetMetric() {
		payload.Samples = append(payload.Samples, mapMetricSamplePayload(metric, metricType))
	}

	return payload
}

func resolveMetricType(metricFamily *dto.MetricFamily) (dto.MetricType, string) {
	if metricFamily.GetType() == dto.MetricType_COUNTER && metricFamily.Type == nil {
		return dto.MetricType_UNTYPED, "unknown"
	}

	metricType := metricFamily.GetType()

	return metricType, strings.ToLower(metricType.String())
}

func mapMetricSamplePayload(metric *dto.Metric, metricType dto.MetricType) selfMetricSamplePayload {
	payload := selfMetricSamplePayload{
		Labels: mapMetricLabels(metric.GetLabel()),
	}

	switch metricType { //nolint:exhaustive // not need
	case dto.MetricType_COUNTER:
		counter := metric.GetCounter()
		if counter != nil {
			payload.Value = float64Pointer(counter.GetValue())
		}
	case dto.MetricType_GAUGE:
		gauge := metric.GetGauge()
		if gauge != nil {
			payload.Value = float64Pointer(gauge.GetValue())
		}
	case dto.MetricType_UNTYPED:
		untyped := metric.GetUntyped()
		if untyped != nil {
			payload.Value = float64Pointer(untyped.GetValue())
		}
	case dto.MetricType_HISTOGRAM:
		histogram := metric.GetHistogram()
		if histogram != nil {
			payload.SampleCount = uint64Pointer(histogram.GetSampleCount())
			payload.SampleSum = float64Pointer(histogram.GetSampleSum())
			payload.Buckets = mapMetricBuckets(histogram.GetBucket())
		}
	case dto.MetricType_SUMMARY:
		summary := metric.GetSummary()
		if summary != nil {
			payload.SampleCount = uint64Pointer(summary.GetSampleCount())
			payload.SampleSum = float64Pointer(summary.GetSampleSum())
			payload.Quantiles = mapMetricQuantiles(summary.GetQuantile())
		}
	}

	return payload
}

func mapMetricLabels(rawLabels []*dto.LabelPair) map[string]string {
	if len(rawLabels) == 0 {
		return nil
	}

	labels := make(map[string]string, len(rawLabels))
	for _, rawLabel := range rawLabels {
		labelName := rawLabel.GetName()
		if labelName == "" {
			continue
		}
		labels[labelName] = rawLabel.GetValue()
	}
	if len(labels) == 0 {
		return nil
	}

	return labels
}

func mapMetricBuckets(rawBuckets []*dto.Bucket) []selfMetricBucketPayload {
	if len(rawBuckets) == 0 {
		return nil
	}

	buckets := make([]selfMetricBucketPayload, 0, len(rawBuckets))
	for _, rawBucket := range rawBuckets {
		buckets = append(buckets, selfMetricBucketPayload{
			UpperBound:      rawBucket.GetUpperBound(),
			CumulativeCount: rawBucket.GetCumulativeCount(),
		})
	}

	return buckets
}

func mapMetricQuantiles(rawQuantiles []*dto.Quantile) []selfMetricQuantilePayload {
	if len(rawQuantiles) == 0 {
		return nil
	}

	quantiles := make([]selfMetricQuantilePayload, 0, len(rawQuantiles))
	for _, rawQuantile := range rawQuantiles {
		quantiles = append(quantiles, selfMetricQuantilePayload{
			Quantile: rawQuantile.GetQuantile(),
			Value:    rawQuantile.GetValue(),
		})
	}

	return quantiles
}

func float64Pointer(value float64) *float64 {
	return &value
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}

type selfMetricsListPayload struct {
	// Metrics contains current application metrics snapshot.
	Metrics []selfMetricPayload `json:"metrics"`
}

type selfMetricPayload struct {
	// Name contains a metric family name.
	Name string `json:"name"`

	// Help contains metric family description.
	Help string `json:"help,omitempty"`

	// Type contains metric type, for example counter or histogram.
	Type string `json:"type"`

	// Samples contains metric samples for this family.
	Samples []selfMetricSamplePayload `json:"samples"`
}

type selfMetricSamplePayload struct {
	// Labels contains metric sample labels.
	Labels map[string]string `json:"labels,omitempty"`

	// Value contains scalar sample value for counter, gauge or untyped metric.
	Value *float64 `json:"value,omitempty"`

	// SampleCount contains total number of observations for histogram or summary.
	SampleCount *uint64 `json:"sampleCount,omitempty"`

	// SampleSum contains observed value sum for histogram or summary.
	SampleSum *float64 `json:"sampleSum,omitempty"`

	// Buckets contains histogram buckets with cumulative counters.
	Buckets []selfMetricBucketPayload `json:"buckets,omitempty"`

	// Quantiles contains summary quantiles.
	Quantiles []selfMetricQuantilePayload `json:"quantiles,omitempty"`
}

type selfMetricBucketPayload struct {
	// UpperBound contains upper bucket limit.
	UpperBound float64 `json:"upperBound"`

	// CumulativeCount contains cumulative sample count in bucket.
	CumulativeCount uint64 `json:"cumulativeCount"`
}

type selfMetricQuantilePayload struct {
	// Quantile contains quantile rank in range [0, 1].
	Quantile float64 `json:"quantile"`

	// Value contains quantile value.
	Value float64 `json:"value"`
}
