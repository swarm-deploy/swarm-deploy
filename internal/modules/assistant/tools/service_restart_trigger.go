package tools

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// RestartService restarts stack service by scaling replicas to zero and restoring previous value.
type RestartService struct {
	manager         swarm.ServiceManager
	eventDispatcher dispatcher.Dispatcher
}

type restartServiceRequest struct {
	Stack   string `json:"stack"`
	Service string `json:"service"`
}

// NewRestartService creates service_restart_trigger component.
func NewRestartService(manager swarm.ServiceManager, eventDispatcher dispatcher.Dispatcher) *RestartService {
	return &RestartService{
		manager:         manager,
		eventDispatcher: eventDispatcher,
	}
}

// Definition returns tool metadata visible to the model.
func (s *RestartService) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        config.AssistantToolNameServiceRestartTrigger,
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
		Request: restartServiceRequest{},
	}
}

// Execute runs service_restart_trigger tool.
func (s *RestartService) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[restartServiceRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	target, err := parseServiceReplicasTarget(parsedRequest.Stack, parsedRequest.Service)
	if err != nil {
		return routing.Response{}, err
	}

	replicas, err := s.manager.Restart(ctx, target)
	if err != nil {
		return routing.Response{}, fmt.Errorf("restart service: %w", err)
	}

	err = s.eventDispatcher.Publish(ctx, &events.ServiceRestarted{
		StackName:   target.StackName(),
		ServiceName: target.ServiceName(),
	})

	if err != nil {
		return routing.Response{}, fmt.Errorf("service restarted but event persistence failed: %w", err)
	}
	payload := ServiceActionResult{
		Stack:    target.StackName(),
		Service:  target.ServiceName(),
		Replicas: replicas,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
