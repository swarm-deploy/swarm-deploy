package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListHistoryEventsExecute(t *testing.T) {
	tool := NewListHistoryEvents(&fakeHistoryStore{
		entries: []history.Entry{
			{Type: events.TypeDeploySuccess, CreatedAt: time.Unix(1, 0), Message: "1"},
			{Type: events.TypeDeployFailed, CreatedAt: time.Unix(2, 0), Message: "2"},
			{Type: events.TypeSyncManualStarted, CreatedAt: time.Unix(3, 0), Message: "3"},
		},
	})

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"limit": float64(2),
		},
	})
	require.NoError(t, err, "execute list_history_events")

	var payload struct {
		Events []history.Entry `json:"events"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Events, 2, "expected limited response")
	assert.Equal(t, "2", payload.Events[0].Message, "expected latest events slice")
	assert.Equal(t, "3", payload.Events[1].Message, "expected latest events slice")
}

func TestListHistoryEventsExecuteFailsOnInvalidLimit(t *testing.T) {
	tool := NewListHistoryEvents(&fakeHistoryStore{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"limit": "abc",
		},
	})
	require.Error(t, err, "expected parse error")
	assert.Contains(t, err.Error(), "limit must be integer", "unexpected error")
}
