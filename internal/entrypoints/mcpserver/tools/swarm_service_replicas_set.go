package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// SetServiceReplicas updates service replicas count.
type SetServiceReplicas struct {
	manager ServiceReplicasManager
}

// NewSetServiceReplicas creates swarm_service_replicas_set component.
func NewSetServiceReplicas(manager ServiceReplicasManager) *SetServiceReplicas {
	return &SetServiceReplicas{
		manager: manager,
	}
}

// Definition returns tool metadata visible to the model.
func (s *SetServiceReplicas) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "swarm_service_replicas_set",
		Description: "Updates replicas count for a stack service.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"stack",
				"service",
				"replicas",
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
				"replicas": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"description": "Desired replicas count (>0).",
				},
			},
		},
	}
}

// Execute runs swarm_service_replicas_set tool.
func (s *SetServiceReplicas) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	target, err := parseServiceReplicasTarget(request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	replicas, err := parseReplicasParam(request.Payload["replicas"])
	if err != nil {
		return routing.Response{}, err
	}

	err = s.manager.UpdateServiceReplicas(ctx, target.stack, target.service, replicas)
	if err != nil {
		return routing.Response{}, fmt.Errorf("update service replicas: %w", err)
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
