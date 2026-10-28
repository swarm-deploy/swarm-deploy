package logparser

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestServiceGetParsesAndFiltersLevels(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	source := swarm.NewMockServiceManager(ctrl)
	service := NewService(source)
	since := time.Date(2026, time.October, 19, 18, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)

	entries := make(chan swarm.LogEntry, 3)
	errorsChannel := make(chan error)
	entries <- swarm.LogEntry{Timestamp: since, Stream: "stdout", Message: `{"level":"info","message":"ready"}`}
	entries <- swarm.LogEntry{Timestamp: since.Add(time.Minute), Stream: "stderr", Message: `level=error msg="failed" code=E_CONN`}
	entries <- swarm.LogEntry{Timestamp: since.Add(2 * time.Minute), Stream: "stdout", Message: "plain fallback"}
	close(entries)
	close(errorsChannel)

	source.EXPECT().TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{
		Follow: false,
		Limit:  25,
		Since:  &since,
		Until:  &until,
	}).Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errorsChannel), nil)

	result, err := service.Get(context.Background(), "task-1", Query{
		Limit:  25,
		Since:  &since,
		Until:  &until,
		Parse:  true,
		Levels: []Level{LevelError},
	})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.Equal(t, LevelError, result.Entries[0].Level)
	assert.Equal(t, "failed", result.Entries[0].Message)
	assert.Equal(t, "stderr", result.Entries[0].Stream)
	assert.Equal(t, map[string]any{"code": "E_CONN"}, result.Entries[0].Attributes)
	assert.Equal(t, 2, result.Counts[LevelInfo], "plain fallback and JSON info must be counted")
	assert.Equal(t, 1, result.Counts[LevelError])
}

func TestServiceGetKeepsRawCompatibilityWhenParsingDisabled(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	source := swarm.NewMockServiceManager(ctrl)
	service := NewService(source)
	timestamp := time.Date(2026, time.October, 19, 18, 0, 0, 0, time.UTC)

	entries := make(chan swarm.LogEntry, 1)
	errorsChannel := make(chan error)
	entries <- swarm.LogEntry{Timestamp: timestamp, Stream: "stdout", Message: `{"level":"error","message":"unchanged"}`}
	close(entries)
	close(errorsChannel)

	source.EXPECT().TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{Limit: 1}).
		Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errorsChannel), nil)

	result, err := service.Get(context.Background(), "task-1", Query{Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.Empty(t, result.Entries[0].Level)
	assert.Nil(t, result.Entries[0].Attributes)
	assert.Equal(t, `{"level":"error","message":"unchanged"}`, result.Entries[0].Message)
}

func TestServiceGetFiltersByAllAttributes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		attributes map[string]any
		messages   []string
	}{
		{
			name:       "all pairs match",
			attributes: map[string]any{"component": "api", "status": float64(200)},
			messages:   []string{"match"},
		},
		{
			name:       "one pair differs",
			attributes: map[string]any{"component": "api", "status": json.Number("500")},
			messages:   []string{},
		},
		{
			name:       "empty filter accepts all entries",
			attributes: map[string]any{},
			messages:   []string{"match", "wrong value", "missing key"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			source := swarm.NewMockServiceManager(ctrl)
			service := NewService(source)
			entries := make(chan swarm.LogEntry, 3)
			errorsChannel := make(chan error)
			entries <- swarm.LogEntry{Message: `{"message":"match","component":"api","status":200.0}`}
			entries <- swarm.LogEntry{Message: `{"message":"wrong value","component":"worker","status":200}`}
			entries <- swarm.LogEntry{Message: `{"message":"missing key","component":"api"}`}
			close(entries)
			close(errorsChannel)

			source.EXPECT().TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{Limit: 3}).
				Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errorsChannel), nil)

			result, err := service.Get(context.Background(), "task-1", Query{
				Limit:      3,
				Parse:      true,
				Attributes: test.attributes,
			})
			require.NoError(t, err)

			messages := make([]string, 0, len(result.Entries))
			for _, entry := range result.Entries {
				messages = append(messages, entry.Message)
			}
			assert.Equal(t, test.messages, messages)
		})
	}
}
