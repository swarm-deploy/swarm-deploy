package tools

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	resourcegraph "github.com/swarm-deploy/swarm-deploy/internal/resources/graph"
)

// GetDependencyGraph returns service dependency graph built from service metadata.
type GetDependencyGraph struct {
	services ServicesReader
}

// NewGetDependencyGraph creates dependency_graph_get component.
func NewGetDependencyGraph(services ServicesReader) *GetDependencyGraph {
	return &GetDependencyGraph{
		services: services,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GetDependencyGraph) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "dependency_graph_get",
		Description: "Returns service dependency graph with nodes, endpoints, and direct dependencies.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Request: struct{}{},
	}
}

// Execute runs dependency_graph_get tool.
func (g *GetDependencyGraph) Execute(_ context.Context, _ routing.Request) (routing.Response, error) {
	built := resourcegraph.NewBuilder().Build(g.services.List())

	payload := struct {
		// Nodes contains graph nodes with dependencies and endpoints.
		Nodes []resourcegraph.Node `json:"nodes"`
	}{
		Nodes: built.Nodes,
	}

	return routing.Response{Payload: payload}, nil
}
