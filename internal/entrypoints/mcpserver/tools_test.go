package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/swarm"
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

type fakeNodeStore struct {
	nodes []swarm.NodeInfo
}

func (f *fakeNodeStore) List() []swarm.NodeInfo {
	out := make([]swarm.NodeInfo, len(f.nodes))
	copy(out, f.nodes)
	return out
}

func TestToolsExecuteListHistoryEvents(t *testing.T) {
	historyStore := &fakeHistoryStore{
		entries: []history.Entry{
			{Type: events.TypeDeploySuccess, CreatedAt: time.Unix(1, 0), Message: "1"},
			{Type: events.TypeDeployFailed, CreatedAt: time.Unix(2, 0), Message: "2"},
			{Type: events.TypeSyncManualStarted, CreatedAt: time.Unix(3, 0), Message: "3"},
		},
	}
	tools := NewTools(historyStore, &fakeNodeStore{}, &fakeSyncControl{})

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
	tools := NewTools(&fakeHistoryStore{}, &fakeNodeStore{}, control)

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
	tools := NewTools(&fakeHistoryStore{}, &fakeNodeStore{}, &fakeSyncControl{})

	_, err := tools.Execute(context.Background(), "list_history_events", map[string]any{
		"limit": "abc",
	})
	require.Error(t, err, "expected parse error")
	assert.Contains(t, err.Error(), "limit must be integer", "unexpected error")
}

func TestToolsExecuteListNodes(t *testing.T) {
	tools := NewTools(&fakeHistoryStore{}, &fakeNodeStore{
		nodes: []swarm.NodeInfo{
			{
				ID:            "node-1",
				Hostname:      "manager-1",
				Status:        "ready",
				Availability:  "active",
				ManagerStatus: "leader",
				EngineVersion: "28.3.0",
				Addr:          "10.0.0.1",
			},
		},
	}, &fakeSyncControl{})

	raw, err := tools.Execute(context.Background(), "list_nodes", nil)
	require.NoError(t, err, "execute list_nodes tool")

	var payload struct {
		Nodes []swarm.NodeInfo `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &payload), "decode response")
	require.Len(t, payload.Nodes, 1, "expected one node")
	assert.Equal(t, "node-1", payload.Nodes[0].ID, "unexpected node id")
	assert.Equal(t, "manager-1", payload.Nodes[0].Hostname, "unexpected hostname")
}
