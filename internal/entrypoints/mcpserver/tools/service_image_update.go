package tools

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/serviceupdater"
)

// ServiceImageUpdate updates image version for a service in push repository.
type ServiceImageUpdate struct {
	updater ServiceUpdater
}

type updateServiceImageRequest struct {
	Stack        string `json:"stack"`
	Service      string `json:"service"`
	ImageVersion string `json:"imageVersion"`
	Reason       string `json:"reason"`
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
	parsedRequest, err := convertRequestPayload[updateServiceImageRequest](request)
	if err != nil {
		return routing.Response{}, err
	}

	userName := "unknown-user"
	if user, ok := security.UserFromContext(ctx); ok && user.Name != "" {
		userName = user.Name
	}

	result, err := s.updater.UpdateImageVersion(ctx, serviceupdater.UpdateImageVersionInput{
		StackName:    parsedRequest.Stack,
		ServiceName:  parsedRequest.Service,
		ImageVersion: parsedRequest.ImageVersion,
		Reason:       parsedRequest.Reason,
		UserName:     userName,
	})
	if err != nil {
		return routing.Response{}, err
	}

	return routing.Response{
		Payload: result,
	}, nil
}
