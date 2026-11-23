package tools

import (
	"context"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/imageupdate"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

type serviceImageUpdate struct {
	updater *imageupdate.Service
}

type serviceImageUpdateRequest struct {
	Stack   string `json:"stack"`
	Service string `json:"service"`
	Version string `json:"imageVersion"`
}

func (s *serviceImageUpdate) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        config.AssistantToolNameServiceImageUpdate,
		Writable:    true,
		Terminal:    true,
		Description: "Propose a GitOps service image version change on a Git branch.",
		Request:     serviceImageUpdateRequest{},
	}
}

func (s *serviceImageUpdate) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	input, err := convertRequestPayload[serviceImageUpdateRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	author := "swarm-deploy"
	if user, ok := security.UserFromContext(ctx); ok && strings.TrimSpace(user.Name) != "" {
		author = strings.Join(strings.Fields(user.Name), " ")
	}
	result, err := s.updater.Update(ctx, input.Stack, input.Service, input.Version, author)
	if err != nil {
		return routing.Response{}, err
	}
	return routing.Response{Payload: result}, nil
}
