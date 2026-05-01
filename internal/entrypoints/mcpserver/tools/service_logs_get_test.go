package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestGetServiceLogsExecuteAppliesTimePagination(t *testing.T) {
	logInspector := &fakeServiceLogsInspector{
		logs: []string{
			"2026-04-18T12:00:00Z stdout oldest",
			"2026-04-18T12:00:01Z stdout middle",
			"2026-04-18T12:00:02Z stdout latest",
		},
	}
	tool := NewGetServiceLogs(logInspector)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
			Limit:       intPointer(2),
		},
	})
	require.NoError(t, err, "execute service_logs_get")

	var payload struct {
		StackName    string   `json:"stack_name"`
		ServiceName  string   `json:"service_name"`
		Logs         []string `json:"logs"`
		Count        int      `json:"count"`
		AppliedSince string   `json:"applied_since"`
		AppliedUntil string   `json:"applied_until"`
		HasMore      bool     `json:"has_more"`
		NextUntil    string   `json:"next_until"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "core", payload.StackName, "unexpected stack name")
	assert.Equal(t, "api", payload.ServiceName, "unexpected service name")
	assert.Equal(t, []string{
		"2026-04-18T12:00:01Z stdout middle",
		"2026-04-18T12:00:02Z stdout latest",
	}, payload.Logs, "unexpected logs page")
	assert.Equal(t, 2, payload.Count, "unexpected log line count")
	assert.Empty(t, payload.AppliedSince, "unexpected applied_since")
	assert.Empty(t, payload.AppliedUntil, "unexpected applied_until")
	assert.True(t, payload.HasMore, "expected older logs availability")
	assert.Equal(t, "2026-04-18T12:00:01Z", payload.NextUntil, "unexpected next_until")

	assert.Equal(t, 1, logInspector.called, "inspector must be called once")
	assert.Equal(t, "core", logInspector.stackName, "unexpected stack arg")
	assert.Equal(t, "api", logInspector.serviceName, "unexpected service arg")
	assert.Equal(t, 3, logInspector.options.Limit, "unexpected inspector limit")
	assert.Nil(t, logInspector.options.Since, "unexpected inspector since")
	assert.Nil(t, logInspector.options.Until, "unexpected inspector until")
}

func TestGetServiceLogsExecuteWithSinceUntil(t *testing.T) {
	logInspector := &fakeServiceLogsInspector{
		logs: []string{
			"2026-04-18T12:00:01Z stdout one",
		},
	}
	tool := NewGetServiceLogs(logInspector)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
			Since:       "2026-04-18T12:00:00+03:00",
			Until:       "2026-04-18T12:05:00+03:00",
			Limit:       intPointer(5),
		},
	})
	require.NoError(t, err, "execute service_logs_get with since/until")

	var payload struct {
		AppliedSince string `json:"applied_since"`
		AppliedUntil string `json:"applied_until"`
		HasMore      bool   `json:"has_more"`
		NextUntil    string `json:"next_until"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "2026-04-18T09:00:00Z", payload.AppliedSince, "unexpected applied since")
	assert.Equal(t, "2026-04-18T09:05:00Z", payload.AppliedUntil, "unexpected applied until")
	assert.False(t, payload.HasMore, "expected no older logs")
	assert.Empty(t, payload.NextUntil, "unexpected next_until")

	require.NotNil(t, logInspector.options.Since, "since must be forwarded to inspector")
	require.NotNil(t, logInspector.options.Until, "until must be forwarded to inspector")
	assert.Equal(
		t,
		"2026-04-18T09:00:00Z",
		logInspector.options.Since.UTC().Format(time.RFC3339Nano),
		"unexpected inspector since",
	)
	assert.Equal(
		t,
		"2026-04-18T09:04:59.999999999Z",
		logInspector.options.Until.UTC().Format(time.RFC3339Nano),
		"unexpected exclusive inspector until",
	)
	assert.Equal(t, 6, logInspector.options.Limit, "unexpected inspector limit")
}

func TestGetServiceLogsExecuteWithNilInspector(t *testing.T) {
	tool := NewGetServiceLogs(nil)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
		},
	})
	require.Error(t, err, "expected nil inspector error")
	assert.Contains(t, err.Error(), "service logs inspector is not configured", "unexpected error")
}

func TestGetServiceLogsExecuteRequiresStackName(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			ServiceName: "api",
		},
	})
	require.Error(t, err, "expected stack_name required error")
	assert.Contains(t, err.Error(), "stack_name is required", "unexpected error")
}

func TestGetServiceLogsExecuteRequiresServiceName(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName: "core",
		},
	})
	require.Error(t, err, "expected service_name required error")
	assert.Contains(t, err.Error(), "service_name is required", "unexpected error")
}

func TestGetServiceLogsExecuteFailsOnInvalidLimit(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
			Limit:       intPointer(0),
		},
	})
	require.Error(t, err, "expected invalid limit error")
	assert.Contains(t, err.Error(), "limit must be > 0", "unexpected error")
}

func TestGetServiceLogsExecuteFailsWhenLimitIsTooHigh(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
			Limit:       intPointer(maxServiceLogsLimit + 1),
		},
	})
	require.Error(t, err, "expected too high limit error")
	assert.Contains(t, err.Error(), "limit must be <=", "unexpected error")
}

func TestGetServiceLogsExecuteFailsOnInvalidSince(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
			Since:       "invalid",
		},
	})
	require.Error(t, err, "expected invalid since error")
	assert.Contains(t, err.Error(), "since must be RFC3339/RFC3339Nano timestamp", "unexpected error")
}

func TestGetServiceLogsExecuteFailsWhenSinceAfterUntil(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
			Since:       "2026-04-18T12:10:00Z",
			Until:       "2026-04-18T12:00:00Z",
		},
	})
	require.Error(t, err, "expected invalid time window error")
	assert.Contains(t, err.Error(), "since must be before or equal to until", "unexpected error")
}

func TestGetServiceLogsExecuteFailsWhenOldestLogHasNoTimestamp(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{
		logs: []string{
			"2026-04-18T12:00:00Z stdout older",
			"broken-log-line",
		},
	})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
			Limit:       intPointer(1),
		},
	})
	require.Error(t, err, "expected invalid oldest timestamp error")
	assert.Contains(t, err.Error(), "parse oldest log timestamp", "unexpected error")
}

func TestGetServiceLogsExecuteReturnsInspectorError(t *testing.T) {
	tool := NewGetServiceLogs(&fakeServiceLogsInspector{
		err: assert.AnError,
	})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
		},
	})
	require.Error(t, err, "expected inspector error")
	assert.ErrorIs(t, err, assert.AnError, "unexpected inspector error")
}

type fakeServiceLogsInspector struct {
	logs []string
	err  error

	called      int
	stackName   string
	serviceName string
	options     swarm.ServiceLogsOptions
}

func (f *fakeServiceLogsInspector) Logs(
	_ context.Context,
	serviceRef swarm.ServiceReference,
	options swarm.ServiceLogsOptions,
) ([]string, error) {
	f.called++
	f.stackName = serviceRef.StackName()
	f.serviceName = serviceRef.ServiceName()
	f.options = options

	if f.err != nil {
		return nil, f.err
	}

	out := make([]string, len(f.logs))
	copy(out, f.logs)

	return out, nil
}
