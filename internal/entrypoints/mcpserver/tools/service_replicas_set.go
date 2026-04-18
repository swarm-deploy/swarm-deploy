package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
)

// SetServiceReplicas updates service replicas count.
type SetServiceReplicas struct {
	manager         ServiceReplicasManager
	eventDispatcher dispatcher.Dispatcher
}

// NewSetServiceReplicas creates service_replicas_set component.
func NewSetServiceReplicas(manager ServiceReplicasManager, eventDispatcher dispatcher.Dispatcher) *SetServiceReplicas {
	return &SetServiceReplicas{
		manager:         manager,
		eventDispatcher: eventDispatcher,
	}
}

// Definition returns tool metadata visible to the model.
func (s *SetServiceReplicas) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "service_replicas_set",
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

// Execute runs service_replicas_set tool.
func (s *SetServiceReplicas) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	target, err := parseServiceReplicasTarget(request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	replicas, err := parseReplicasParam(request.Payload["replicas"])
	if err != nil {
		return routing.Response{}, err
	}

	currentReplicas, err := s.manager.InspectServiceReplicas(ctx, target.stack, target.service)
	if err != nil {
		return routing.Response{}, fmt.Errorf("inspect service replicas: %w", err)
	}

	err = s.manager.UpdateServiceReplicas(ctx, target.stack, target.service, replicas)
	if err != nil {
		return routing.Response{}, fmt.Errorf("update service replicas: %w", err)
	}

	if replicas > currentReplicas {
		s.eventDispatcher.Dispatch(ctx, &events.ServiceReplicasIncreased{
			StackName:        target.stack,
			ServiceName:      target.service,
			PreviousReplicas: currentReplicas,
			CurrentReplicas:  replicas,
		})
	} else if replicas < currentReplicas {
		s.eventDispatcher.Dispatch(ctx, &events.ServiceReplicasDecreased{
			StackName:        target.stack,
			ServiceName:      target.service,
			PreviousReplicas: currentReplicas,
			CurrentReplicas:  replicas,
		})
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
