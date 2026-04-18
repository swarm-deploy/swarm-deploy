package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
)

// GetServiceSpec returns compact service spec projection from Docker Swarm.
type GetServiceSpec struct {
	inspector ServiceSpecInspector
}

// NewGetServiceSpec creates service_spec_get component.
func NewGetServiceSpec(specInspector ServiceSpecInspector) *GetServiceSpec {
	return &GetServiceSpec{
		inspector: specInspector,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GetServiceSpec) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "service_spec_get",
		Description: "Returns compact service projection (service metadata, current and previous spec) for a stack service.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"stack_name",
				"service_name",
			},
			"properties": map[string]any{
				"stack_name": map[string]any{
					"type":        "string",
					"description": "Docker Swarm stack name.",
				},
				"service_name": map[string]any{
					"type":        "string",
					"description": "Service name inside the stack.",
				},
			},
		},
	}
}

// Execute runs service_spec_get tool.
func (g *GetServiceSpec) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	stackName, err := parseStringParam(request.Payload["stack_name"], "stack_name")
	if err != nil {
		return routing.Response{}, err
	}
	if stackName == "" {
		return routing.Response{}, fmt.Errorf("stack_name is required")
	}

	serviceName, err := parseStringParam(request.Payload["service_name"], "service_name")
	if err != nil {
		return routing.Response{}, err
	}
	if serviceName == "" {
		return routing.Response{}, fmt.Errorf("service_name is required")
	}

	service, err := g.inspector.InspectServiceSpec(ctx, stackName, serviceName)
	if err != nil {
		return routing.Response{}, err
	}

	payload := struct {
		// StackName is a target stack name.
		StackName string `json:"stack_name"`
		// ServiceName is a target service name.
		ServiceName string `json:"service_name"`
		// Service contains compact service projection.
		Service inspector.Service `json:"service"`
	}{
		StackName:   stackName,
		ServiceName: serviceName,
		Service:     service,
	}

	return routing.Response{Payload: payload}, nil
}
