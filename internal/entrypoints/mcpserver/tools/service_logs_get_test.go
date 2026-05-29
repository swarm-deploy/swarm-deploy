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
	"go.uber.org/mock/gomock"
)

func TestGetServiceLogsExecuteAppliesTimePagination(t *testing.T) {
	ctrl := gomock.NewController(t)
	logInspector := swarm.NewMockServiceManager(ctrl)
	tool := NewGetServiceLogs(logInspector)

	expectedServiceRef := swarm.NewServiceReference("core", "api")
	expectedOptions := swarm.ServiceLogsOptions{
		Limit: 3,
	}
	logInspector.EXPECT().
		Logs(gomock.Any(), expectedServiceRef, expectedOptions).
		Return([]string{
			"2026-04-18T12:00:00Z stdout oldest",
			"2026-04-18T12:00:01Z stdout middle",
			"2026-04-18T12:00:02Z stdout latest",
		}, nil)

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
}

func TestGetServiceLogsExecuteWithSinceUntil(t *testing.T) {
	ctrl := gomock.NewController(t)
	logInspector := swarm.NewMockServiceManager(ctrl)
	tool := NewGetServiceLogs(logInspector)

	var (
		actualServiceRef swarm.ServiceReference
		actualOptions    swarm.ServiceLogsOptions
	)
	logInspector.EXPECT().
		Logs(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			serviceRef swarm.ServiceReference,
			options swarm.ServiceLogsOptions,
		) ([]string, error) {
			actualServiceRef = serviceRef
			actualOptions = options

			return []string{
				"2026-04-18T12:00:01Z stdout one",
			}, nil
		})

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

	assert.Equal(t, swarm.NewServiceReference("core", "api"), actualServiceRef, "unexpected service ref")
	require.NotNil(t, actualOptions.Since, "since must be forwarded to inspector")
	require.NotNil(t, actualOptions.Until, "until must be forwarded to inspector")
	assert.Equal(
		t,
		"2026-04-18T09:00:00Z",
		actualOptions.Since.UTC().Format(time.RFC3339Nano),
		"unexpected inspector since",
	)
	assert.Equal(
		t,
		"2026-04-18T09:04:59.999999999Z",
		actualOptions.Until.UTC().Format(time.RFC3339Nano),
		"unexpected exclusive inspector until",
	)
	assert.Equal(t, 6, actualOptions.Limit, "unexpected inspector limit")
}

func TestGetServiceLogsExecuteRequiresStackName(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := NewGetServiceLogs(swarm.NewMockServiceManager(ctrl))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			ServiceName: "api",
		},
	})
	require.Error(t, err, "expected stack_name required error")
	assert.Contains(t, err.Error(), "stack_name is required", "unexpected error")
}

func TestGetServiceLogsExecuteRequiresServiceName(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := NewGetServiceLogs(swarm.NewMockServiceManager(ctrl))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName: "core",
		},
	})
	require.Error(t, err, "expected service_name required error")
	assert.Contains(t, err.Error(), "service_name is required", "unexpected error")
}

func TestGetServiceLogsExecuteFailsOnInvalidLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := NewGetServiceLogs(swarm.NewMockServiceManager(ctrl))

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
	ctrl := gomock.NewController(t)
	tool := NewGetServiceLogs(swarm.NewMockServiceManager(ctrl))

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
	ctrl := gomock.NewController(t)
	tool := NewGetServiceLogs(swarm.NewMockServiceManager(ctrl))

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
	ctrl := gomock.NewController(t)
	tool := NewGetServiceLogs(swarm.NewMockServiceManager(ctrl))

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
	ctrl := gomock.NewController(t)
	logInspector := swarm.NewMockServiceManager(ctrl)
	tool := NewGetServiceLogs(logInspector)

	logInspector.EXPECT().
		Logs(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]string{
			"2026-04-18T12:00:00Z stdout older",
			"broken-log-line",
		}, nil)

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
	ctrl := gomock.NewController(t)
	logInspector := swarm.NewMockServiceManager(ctrl)
	tool := NewGetServiceLogs(logInspector)

	logInspector.EXPECT().
		Logs(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, assert.AnError)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceLogsRequest{
			StackName:   "core",
			ServiceName: "api",
		},
	})
	require.Error(t, err, "expected inspector error")
	assert.ErrorIs(t, err, assert.AnError, "unexpected inspector error")
}
