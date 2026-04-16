package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// GetServiceReplicas returns current service replicas count.
type GetServiceReplicas struct {
	manager ServiceReplicasManager
}

// NewGetServiceReplicas creates swarm_service_replicas_get component.
func NewGetServiceReplicas(manager ServiceReplicasManager) *GetServiceReplicas {
	return &GetServiceReplicas{
		manager: manager,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GetServiceReplicas) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "swarm_service_replicas_get",
		Description: "Returns current replicas count for a stack service.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"stack",
				"service",
			},
			"properties": map[string]any{
				"stack": map[string]any{
					"type":        "string",
					"description": "Stack name.",
				},
				"service": map[string]any{
					"type":        "string",
					"description": "Service name inside stack.",
				},
			},
		},
	}
}

// Execute runs swarm_service_replicas_get tool.
func (g *GetServiceReplicas) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	if g.manager == nil {
		return routing.Response{}, fmt.Errorf("service replicas manager is not configured")
	}

	target, err := parseServiceReplicasTarget(request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	replicas, err := g.manager.InspectServiceReplicas(ctx, target.stack, target.service)
	if err != nil {
		return routing.Response{}, fmt.Errorf("inspect service replicas: %w", err)
	}

	payload := struct {
		Stack    string `json:"stack"`
		Service  string `json:"service"`
		Replicas uint64 `json:"replicas"`
	}{
		Stack:    target.stack,
		Service:  target.service,
		Replicas: replicas,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
