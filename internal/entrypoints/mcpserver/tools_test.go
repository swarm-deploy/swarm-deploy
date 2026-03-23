package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHistoryStore struct {
	entries []history.Entry
}

func (f *fakeHistoryStore) List() []history.Entry {
	out := make([]history.Entry, len(f.entries))
	copy(out, f.entries)
	return out
}

type fakeSyncControl struct {
	queued bool
	called int
}

func (f *fakeSyncControl) Trigger(_ controller.TriggerReason) bool {
	f.called++
	return f.queued
}

func TestToolsExecuteListHistoryEvents(t *testing.T) {
	historyStore := &fakeHistoryStore{
		entries: []history.Entry{
			{Type: events.TypeDeploySuccess, CreatedAt: time.Unix(1, 0), Message: "1"},
			{Type: events.TypeDeployFailed, CreatedAt: time.Unix(2, 0), Message: "2"},
			{Type: events.TypeSyncManualStarted, CreatedAt: time.Unix(3, 0), Message: "3"},
		},
	}
	tools := NewTools(historyStore, &fakeSyncControl{})

	raw, err := tools.Execute(context.Background(), "list_history_events", map[string]any{
		"limit": float64(2),
	})
	require.NoError(t, err, "execute list_history_events")

	var payload struct {
		Events []history.Entry `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &payload), "decode response")
	require.Len(t, payload.Events, 2, "expected limited response")
	assert.Equal(t, "2", payload.Events[0].Message, "expected latest events slice")
	assert.Equal(t, "3", payload.Events[1].Message, "expected latest events slice")
}

func TestToolsExecuteSync(t *testing.T) {
	control := &fakeSyncControl{queued: true}
	tools := NewTools(&fakeHistoryStore{}, control)

	raw, err := tools.Execute(context.Background(), "sync", nil)
	require.NoError(t, err, "execute sync tool")

	var payload struct {
		Queued bool `json:"queued"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &payload), "decode response")
	assert.True(t, payload.Queued, "expected queued=true response")
	assert.Equal(t, 1, control.called, "expected single trigger call")
}

func TestToolsExecuteFailsOnInvalidLimit(t *testing.T) {
	tools := NewTools(&fakeHistoryStore{}, &fakeSyncControl{})

	_, err := tools.Execute(context.Background(), "list_history_events", map[string]any{
		"limit": "abc",
	})
	require.Error(t, err, "expected parse error")
	assert.Contains(t, err.Error(), "limit must be integer", "unexpected error")
}
