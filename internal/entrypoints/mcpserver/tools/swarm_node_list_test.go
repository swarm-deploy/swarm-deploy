package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListNodesExecute(t *testing.T) {
	tool := NewListNodes(&fakeNodeStore{
		nodes: []inspector.NodeInfo{
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
	})

	response, err := tool.Execute(context.Background(), routing.Request{})
	require.NoError(t, err, "execute swarm_node_list tool")

	var payload struct {
		Nodes []inspector.NodeInfo `json:"nodes"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Nodes, 1, "expected one node")
	assert.Equal(t, "node-1", payload.Nodes[0].ID, "unexpected node id")
	assert.Equal(t, "manager-1", payload.Nodes[0].Hostname, "unexpected hostname")
}
