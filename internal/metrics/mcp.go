package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type MCP interface {
	subsystem

	RecordToolExecution(toolName string, ok bool, duration time.Duration)
	RecordUnknownTool(toolName string)
}

type prometheusMCP struct {
	toolExecutionTotal *prometheus.CounterVec
	toolExecutionTime  *prometheus.HistogramVec
	unknownToolTotal   *prometheus.CounterVec
}

type nopMCP struct{}

func (nopMCP) collectors() []prometheus.Collector {
	return []prometheus.Collector{}
}

func (n nopMCP) RecordToolExecution(string, bool, time.Duration) {}

func (n nopMCP) RecordUnknownTool(string) {}

func newMCP(namespace string, enabled bool) MCP {
	if enabled {
		return newPrometheusMCP(namespace)
	}
	return &nopMCP{}
}

func newPrometheusMCP(namespace string) MCP {
	return &prometheusMCP{
		toolExecutionTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "mcp",
				Name:      "tool_execution_total",
				Help:      "Number of MCP tool executions grouped by tool and ok flag.",
			},
			[]string{"tool", "ok"},
		),
		toolExecutionTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "mcp",
				Name:      "tool_execution_duration_seconds",
				Help:      "MCP tool execution duration in seconds grouped by tool and ok flag.",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
			},
			[]string{"tool", "ok"},
		),
		unknownToolTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "mcp",
				Name:      "tool_unknown_total",
				Help:      "Number of unknown MCP tool invocations grouped by requested tool name.",
			},
			[]string{"tool"},
		),
	}
}

// RecordToolExecution tracks MCP tool execution by tool name and success flag.
func (m *prometheusMCP) RecordToolExecution(toolName string, ok bool, duration time.Duration) {
	okValue := strconv.FormatBool(ok)
	m.toolExecutionTotal.WithLabelValues(toolName, okValue).Inc()
	m.toolExecutionTime.WithLabelValues(toolName, okValue).Observe(duration.Seconds())
}

// RecordUnknownTool tracks calls with an unknown tool name.
func (m *prometheusMCP) RecordUnknownTool(toolName string) {
	m.unknownToolTotal.WithLabelValues(toolName).Inc()
}

func (m *prometheusMCP) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.toolExecutionTime,
		m.toolExecutionTotal,
		m.unknownToolTotal,
	}
}
