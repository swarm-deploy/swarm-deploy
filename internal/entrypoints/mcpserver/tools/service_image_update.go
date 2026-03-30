package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/security"
	"github.com/artarts36/swarm-deploy/internal/serviceupdater"
)

// ServiceImageUpdate updates image version for a service in push repository.
type ServiceImageUpdate struct {
	updater ServiceUpdater
}

// NewServiceImageUpdate creates service_image_update component.
func NewServiceImageUpdate(updater ServiceUpdater) *ServiceImageUpdate {
	return &ServiceImageUpdate{
		updater: updater,
	}
}

// Definition returns tool metadata visible to the model.
func (s *ServiceImageUpdate) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name: "service_image_update",
		Description: "Updates service image version in stack compose file, commits and pushes changes " +
			"to push repository, and creates merge request when supported.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"stack",
				"service",
				"imageVersion",
				"reason",
			},
			"properties": map[string]any{
				"stack": map[string]any{
					"type":        "string",
					"description": "Stack name.",
				},
				"service": map[string]any{
					"type":        "string",
					"description": "Service name inside stack compose file.",
				},
				"imageVersion": map[string]any{
					"type":        "string",
					"description": "Target image version (tag).",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Original user prompt that requested image update.",
				},
			},
		},
	}
}

// Execute runs service_image_update tool.
func (s *ServiceImageUpdate) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	if s.updater == nil {
		return routing.Response{}, fmt.Errorf("service updater is not configured")
	}

	stackName, err := parseStringParam(request.Payload["stack"], "stack")
	if err != nil {
		return routing.Response{}, err
	}
	if stackName == "" {
		return routing.Response{}, fmt.Errorf("stack is required")
	}

	serviceName, err := parseStringParam(request.Payload["service"], "service")
	if err != nil {
		return routing.Response{}, err
	}
	if serviceName == "" {
		return routing.Response{}, fmt.Errorf("service is required")
	}

	imageVersion, err := parseStringParam(request.Payload["imageVersion"], "imageVersion")
	if err != nil {
		return routing.Response{}, err
	}
	if imageVersion == "" {
		return routing.Response{}, fmt.Errorf("imageVersion is required")
	}

	reason, err := parseStringParam(request.Payload["reason"], "reason")
	if err != nil {
		return routing.Response{}, err
	}
	if reason == "" {
		return routing.Response{}, fmt.Errorf("reason is required")
	}

	userName := "unknown-user"
	if user, ok := security.UserFromContext(ctx); ok && user.Name != "" {
		userName = user.Name
	}

	result, err := s.updater.UpdateImageVersion(ctx, serviceupdater.UpdateImageVersionInput{
		StackName:    stackName,
		ServiceName:  serviceName,
		ImageVersion: imageVersion,
		Reason:       reason,
		UserName:     userName,
	})
	if err != nil {
		return routing.Response{}, err
	}

	return routing.Response{
		Payload: result,
	}, nil
}
