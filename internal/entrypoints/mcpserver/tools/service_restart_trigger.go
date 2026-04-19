package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
)

// RestartService restarts stack service by scaling replicas to zero and restoring previous value.
type RestartService struct {
	manager         ServiceReplicasManager
	eventDispatcher dispatcher.Dispatcher
}

// NewRestartService creates service_restart_trigger component.
func NewRestartService(manager ServiceReplicasManager, eventDispatcher dispatcher.Dispatcher) *RestartService {
	return &RestartService{
		manager:         manager,
		eventDispatcher: eventDispatcher,
	}
}

// Definition returns tool metadata visible to the model.
func (s *RestartService) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "service_restart_trigger",
		Description: "Restarts a stack service by scaling replicas to zero and restoring previous count.",
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

// Execute runs service_restart_trigger tool.
func (s *RestartService) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	target, err := parseServiceReplicasTarget(request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	currentReplicas, err := s.manager.InspectServiceReplicas(ctx, target.stack, target.service)
	if err != nil {
		return routing.Response{}, fmt.Errorf("inspect service replicas: %w", err)
	}

	err = s.manager.UpdateServiceReplicas(ctx, target.stack, target.service, 0)
	if err != nil {
		return routing.Response{}, fmt.Errorf("scale service replicas to 0: %w", err)
	}

	err = s.manager.UpdateServiceReplicas(ctx, target.stack, target.service, currentReplicas)
	if err != nil {
		return routing.Response{}, fmt.Errorf("restore service replicas to %d: %w", currentReplicas, err)
	}

	s.eventDispatcher.Dispatch(ctx, &events.ServiceRestarted{
		StackName:        target.stack,
		ServiceName:      target.service,
		PreviousReplicas: currentReplicas,
		CurrentReplicas:  currentReplicas,
	})

	payload := struct {
		Stack            string `json:"stack"`
		Service          string `json:"service"`
		PreviousReplicas uint64 `json:"previous_replicas"`
	}{
		Stack:            target.stack,
		Service:          target.service,
		PreviousReplicas: currentReplicas,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
