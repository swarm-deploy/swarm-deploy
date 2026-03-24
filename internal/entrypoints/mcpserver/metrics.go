package mcpserver

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics stores MCP server telemetry metrics.
type Metrics struct {
	toolExecutionTotal *prometheus.CounterVec
	toolExecutionTime  *prometheus.HistogramVec
	unknownToolTotal   *prometheus.CounterVec
}

// NewMetrics creates MCP server metrics recorder and registers all collectors.
func NewMetrics(namespace string) *Metrics {
	return &Metrics{
		toolExecutionTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "assistant_mcp_tool_execution_total",
				Help:      "Number of MCP tool executions grouped by tool and ok flag.",
			},
			[]string{"tool", "ok"},
		),
		toolExecutionTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "assistant_mcp_tool_execution_duration_seconds",
				Help:      "MCP tool execution duration in seconds grouped by tool and ok flag.",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
			},
			[]string{"tool", "ok"},
		),
		unknownToolTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "assistant_mcp_tool_unknown_total",
				Help:      "Number of unknown MCP tool invocations grouped by requested tool name.",
			},
			[]string{"tool"},
		),
	}
}

func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	m.toolExecutionTotal.Describe(ch)
	m.toolExecutionTime.Describe(ch)
	m.unknownToolTotal.Describe(ch)
}

func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	m.toolExecutionTotal.Collect(ch)
	m.toolExecutionTime.Collect(ch)
	m.unknownToolTotal.Collect(ch)
}

// RecordToolExecution tracks MCP tool execution by tool name and success flag.
func (m *Metrics) RecordToolExecution(toolName string, ok bool, duration time.Duration) {
	okValue := strconv.FormatBool(ok)
	m.toolExecutionTotal.WithLabelValues(toolName, okValue).Inc()
	m.toolExecutionTime.WithLabelValues(toolName, okValue).Observe(duration.Seconds())
}

// RecordUnknownTool tracks calls with an unknown tool name.
func (m *Metrics) RecordUnknownTool(toolName string) {
	m.unknownToolTotal.WithLabelValues(toolName).Inc()
}
