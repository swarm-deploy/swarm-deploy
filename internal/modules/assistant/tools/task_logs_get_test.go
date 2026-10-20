package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestGetTaskLogsExecuteReturnsBoundedStructuredLogs(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	source := swarm.NewMockServiceManager(ctrl)
	tool := NewGetTaskLogs(logparser.NewService(source))
	since := time.Date(2026, time.October, 19, 18, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)

	entries := make(chan swarm.LogEntry, 2)
	errorsChannel := make(chan error)
	entries <- swarm.LogEntry{Timestamp: since, Stream: "stdout", Message: `{"level":"info","message":"ready"}`}
	entries <- swarm.LogEntry{Timestamp: since.Add(time.Minute), Stream: "stderr", Message: `level=error msg="failed" code=E_CONN`}
	close(entries)
	close(errorsChannel)

	source.EXPECT().TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{
		Limit: 2,
		Since: &since,
		Until: &until,
	}).Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errorsChannel), nil)

	response, err := tool.Execute(context.Background(), routing.Request{Payload: getTaskLogsRequest{
		TaskID: " task-1 ",
		Parse:  true,
		Levels: []string{"ERROR"},
		Limit:  intPointer(2),
		Since:  since.Format(time.RFC3339),
		Until:  until.Format(time.RFC3339),
	}})
	require.NoError(t, err)

	var payload struct {
		TaskID       string            `json:"task_id"`
		Parsed       bool              `json:"parsed"`
		Entries      []logparser.Entry `json:"entries"`
		Count        int               `json:"count"`
		LevelCounts  map[string]int    `json:"level_counts"`
		AppliedSince string            `json:"applied_since"`
		AppliedUntil string            `json:"applied_until"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &payload))

	assert.Equal(t, "task-1", payload.TaskID)
	assert.True(t, payload.Parsed)
	assert.Equal(t, 1, payload.Count)
	require.Len(t, payload.Entries, 1)
	assert.Equal(t, logparser.LevelError, payload.Entries[0].Level)
	assert.Equal(t, "failed", payload.Entries[0].Message)
	assert.Equal(t, `level=error msg="failed" code=E_CONN`, payload.Entries[0].RawMessage)
	assert.Equal(t, 1, payload.LevelCounts["INFO"])
	assert.Equal(t, 1, payload.LevelCounts["ERROR"])
	assert.Equal(t, since.Format(time.RFC3339), payload.AppliedSince)
	assert.Equal(t, until.Format(time.RFC3339), payload.AppliedUntil)
	assert.Equal(t, config.AssistantToolNameServiceTaskLogsGet, tool.Definition().Name)
}

func TestGetTaskLogsRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	tool := NewGetTaskLogs(logparser.NewService(swarm.NewMockServiceManager(ctrl)))
	tests := []struct {
		name    string
		request getTaskLogsRequest
		message string
	}{
		{name: "missing task", request: getTaskLogsRequest{}, message: "task_id is required"},
		{name: "zero limit", request: getTaskLogsRequest{TaskID: "task", Limit: intPointer(0)}, message: "limit must be > 0"},
		{name: "excessive limit", request: getTaskLogsRequest{TaskID: "task", Limit: intPointer(maxTaskLogsLimit + 1)}, message: "limit must be <="},
		{name: "levels without parsing", request: getTaskLogsRequest{TaskID: "task", Levels: []string{"ERROR"}}, message: "parse=true"},
		{name: "unknown level", request: getTaskLogsRequest{TaskID: "task", Parse: true, Levels: []string{"CUSTOM"}}, message: "unsupported log level"},
		{name: "invalid range", request: getTaskLogsRequest{TaskID: "task", Since: "2026-10-20T00:00:00Z", Until: "2026-10-19T00:00:00Z"}, message: "since must be before"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := tool.Execute(context.Background(), routing.Request{Payload: test.request})
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.message)
		})
	}
}

func TestBoundTaskLogEntriesLimitsToolPayload(t *testing.T) {
	t.Parallel()

	entries := []logparser.Entry{{
		Message:    string(make([]byte, maxTaskLogFieldBytes+100)),
		RawMessage: string(make([]byte, maxTaskLogFieldBytes+100)),
		Attributes: map[string]any{"payload": string(make([]byte, maxTaskLogFieldBytes+100))},
	}}

	bounded, truncated := boundTaskLogEntries(entries)
	require.Len(t, bounded, 1)
	assert.True(t, truncated)
	assert.LessOrEqual(t, len(bounded[0].Message), maxTaskLogFieldBytes)
	assert.LessOrEqual(t, len(bounded[0].RawMessage), maxTaskLogFieldBytes)
	assert.Equal(t, map[string]any{"_truncated": true}, bounded[0].Attributes)
}

func TestLogToolResultsAreNotTraced(t *testing.T) {
	t.Parallel()

	assert.False(t, toolResultCanBeTraced(string(config.AssistantToolNameServiceLogsGet)))
	assert.False(t, toolResultCanBeTraced(string(config.AssistantToolNameServiceTaskLogsGet)))
	assert.True(t, toolResultCanBeTraced(string(config.AssistantToolNameServiceSpecGet)))
}
