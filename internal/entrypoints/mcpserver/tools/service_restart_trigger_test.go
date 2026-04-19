package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestartServiceExecute(t *testing.T) {
	manager := &fakeServiceReplicasManager{
		replicasByService: map[string]uint64{
			"core_api": 3,
		},
	}
	dispatcher := &fakeEventDispatcher{}
	tool := NewRestartService(manager, dispatcher)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":   "core",
			"service": "api",
		},
	})
	require.NoError(t, err, "execute service_restart_trigger")

	var payload struct {
		Stack            string `json:"stack"`
		Service          string `json:"service"`
		PreviousReplicas uint64 `json:"previous_replicas"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "core", payload.Stack, "unexpected stack")
	assert.Equal(t, "api", payload.Service, "unexpected service")
	assert.Equal(t, uint64(3), payload.PreviousReplicas, "unexpected replicas before restart")
	assert.Equal(t, 1, manager.inspectCalled, "expected single inspect call")
	assert.Equal(t, 2, manager.updateCalled, "expected scale down and restore calls")
	assert.Equal(t, []uint64{0, 3}, manager.updatedHistory, "unexpected replicas update sequence")

	require.Len(t, dispatcher.events, 1, "expected single dispatched event")
	restartEvent, ok := dispatcher.events[0].(*events.ServiceRestarted)
	require.True(t, ok, "expected service restarted event")
	assert.Equal(t, "core", restartEvent.StackName, "unexpected event stack")
	assert.Equal(t, "api", restartEvent.ServiceName, "unexpected event service")
	assert.Equal(t, uint64(3), restartEvent.PreviousReplicas, "unexpected event previous replicas")
	assert.Equal(t, uint64(3), restartEvent.CurrentReplicas, "unexpected event current replicas")
}

func TestRestartServiceExecuteFailsOnInspect(t *testing.T) {
	dispatcher := &fakeEventDispatcher{}
	tool := NewRestartService(&fakeServiceReplicasManager{
		inspectErr: errors.New("inspect unavailable"),
	}, dispatcher)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":   "core",
			"service": "api",
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "inspect service replicas", "unexpected error")
	assert.Empty(t, dispatcher.events, "failed inspect must not dispatch events")
}

func TestRestartServiceExecuteFailsOnRestore(t *testing.T) {
	dispatcher := &fakeEventDispatcher{}
	manager := &fakeServiceReplicasManager{
		replicasByService: map[string]uint64{
			"core_api": 2,
		},
		updateErr:       errors.New("restore failed"),
		updateErrOnCall: 2,
	}
	tool := NewRestartService(manager, dispatcher)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":   "core",
			"service": "api",
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "restore service replicas", "unexpected error")
	assert.Equal(t, 2, manager.updateCalled, "expected scale down and restore calls")
	assert.Equal(t, []uint64{0, 2}, manager.updatedHistory, "unexpected update sequence before failure")
	assert.Empty(t, dispatcher.events, "failed restore must not dispatch events")
}
