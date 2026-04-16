package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetServiceReplicasExecute(t *testing.T) {
	manager := &fakeServiceReplicasManager{
		replicasByService: map[string]uint64{
			"core_api": 3,
		},
	}
	tool := NewGetServiceReplicas(manager)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":   "core",
			"service": "api",
		},
	})
	require.NoError(t, err, "execute swarm_service_replicas_get")

	var payload struct {
		Stack    string `json:"stack"`
		Service  string `json:"service"`
		Replicas uint64 `json:"replicas"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "core", payload.Stack, "unexpected stack")
	assert.Equal(t, "api", payload.Service, "unexpected service")
	assert.Equal(t, uint64(3), payload.Replicas, "unexpected replicas")
	assert.Equal(t, 1, manager.inspectCalled, "expected single inspect call")
	assert.Equal(t, "core", manager.inspectedStack, "unexpected inspected stack")
	assert.Equal(t, "api", manager.inspectedService, "unexpected inspected service")
}

func TestGetServiceReplicasExecuteFailsOnValidation(t *testing.T) {
	tool := NewGetServiceReplicas(&fakeServiceReplicasManager{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack": "core",
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "service is required", "unexpected error")
}

func TestGetServiceReplicasExecuteFailsOnInspect(t *testing.T) {
	tool := NewGetServiceReplicas(&fakeServiceReplicasManager{
		inspectErr: errors.New("docker unavailable"),
	})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":   "core",
			"service": "api",
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "inspect service replicas", "unexpected error")
}
