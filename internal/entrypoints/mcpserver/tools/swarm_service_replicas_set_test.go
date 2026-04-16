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

func TestSetServiceReplicasExecute(t *testing.T) {
	manager := &fakeServiceReplicasManager{}
	tool := NewSetServiceReplicas(manager)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": "5",
		},
	})
	require.NoError(t, err, "execute swarm_service_replicas_set")

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
	assert.Equal(t, uint64(5), payload.Replicas, "unexpected replicas")
	assert.Equal(t, 1, manager.updateCalled, "expected single update call")
	assert.Equal(t, "core", manager.updatedStack, "unexpected updated stack")
	assert.Equal(t, "api", manager.updatedService, "unexpected updated service")
	assert.Equal(t, uint64(5), manager.updatedReplicas, "unexpected updated replicas")
}

func TestSetServiceReplicasExecuteFailsOnValidation(t *testing.T) {
	tool := NewSetServiceReplicas(&fakeServiceReplicasManager{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": 0,
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "replicas must be > 0", "unexpected error")
}

func TestSetServiceReplicasExecuteFailsOnUpdate(t *testing.T) {
	tool := NewSetServiceReplicas(&fakeServiceReplicasManager{
		updateErr: errors.New("docker unavailable"),
	})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": 2,
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "update service replicas", "unexpected error")
}
