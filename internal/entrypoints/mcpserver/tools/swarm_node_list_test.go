package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestListNodesExecute(t *testing.T) {
	tool := NewListNodes(&fakeNodeStore{
		nodes: []swarm.Node{
			{
				ID:            "node-1",
				Hostname:      "manager-1",
				Status:        "ready",
				Availability:  "active",
				ManagerStatus: "leader",
				EngineVersion: "28.3.0",
				Addr:          "10.0.0.1",
				CPUNano:       8_000_000_000,
				MemoryBytes:   34_359_738_368,
			},
		},
	})

	response, err := tool.Execute(context.Background(), routing.Request{})
	require.NoError(t, err, "execute swarm_node_list tool")

	var payload struct {
		Nodes []swarm.Node `json:"nodes"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Nodes, 1, "expected one node")
	assert.Equal(t, "node-1", payload.Nodes[0].ID, "unexpected node id")
	assert.Equal(t, "manager-1", payload.Nodes[0].Hostname, "unexpected hostname")
	assert.Equal(t, int64(8_000_000_000), payload.Nodes[0].CPUNano, "unexpected cpu")
	assert.Equal(t, int64(34_359_738_368), payload.Nodes[0].MemoryBytes, "unexpected memory")
}
