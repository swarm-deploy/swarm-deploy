package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
)

// ListNodes returns current Docker Swarm nodes snapshot.
type ListNodes struct {
	nodes NodesReader
}

// NewListNodes creates swarm_node_list component.
func NewListNodes(nodesStore NodesReader) *ListNodes {
	return &ListNodes{nodes: nodesStore}
}

// Definition returns tool metadata visible to the model.
func (l *ListNodes) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "swarm_node_list",
		Description: "Returns current Docker Swarm nodes snapshot.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// Execute runs swarm_node_list tool.
func (l *ListNodes) Execute(_ context.Context, _ routing.Request) (routing.Response, error) {
	if l.nodes == nil {
		return routing.Response{}, fmt.Errorf("nodes store is not configured")
	}

	payload := struct {
		Nodes []inspector.NodeInfo `json:"nodes"`
	}{
		Nodes: l.nodes.List(),
	}
	return routing.Response{
		Payload: payload,
	}, nil
}
