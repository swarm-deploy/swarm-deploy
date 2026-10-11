package tools

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	resourcegraph "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/graph"
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
		Name:        config.AssistantToolNameDependencyGraphGet,
		Description: "Returns service dependency graph with nodes, endpoints, and direct dependencies.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Request: struct{}{},
	}
}

// Execute runs dependency_graph_get tool.
func (g *GetDependencyGraph) Execute(ctx context.Context, _ routing.Request) (routing.Response, error) {
	services, err := g.services.ReadAll(ctx)
	if err != nil {
		return routing.Response{}, err
	}
	built := resourcegraph.NewBuilder().Build(services)

	payload := struct {
		// Nodes contains graph nodes with dependencies and endpoints.
		Nodes []resourcegraph.Node `json:"nodes"`
	}{
		Nodes: built.Nodes,
	}

	return routing.Response{Payload: payload}, nil
}
