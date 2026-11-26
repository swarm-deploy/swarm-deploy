package tools

import (
	"context"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/imagepin"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
)

type serviceImagePin struct {
	publish func(context.Context, usecase.ServiceImagePinPlan, string) (imagepin.Result, error)
}

func (s *serviceImagePin) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        config.AssistantToolNameServiceImagePin,
		Writable:    true,
		Terminal:    true,
		Description: "Propose pinning a service image to the digest observed on all running Swarm tasks.",
		Request:     usecase.ServiceImagePinPlan{},
	}
}

func (s *serviceImagePin) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	plan, err := convertRequestPayload[usecase.ServiceImagePinPlan](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	author := "swarm-deploy"
	if user, ok := security.UserFromContext(ctx); ok && strings.TrimSpace(user.Name) != "" {
		author = strings.Join(strings.Fields(user.Name), " ")
	}
	result, err := s.publish(ctx, plan, author)
	if err != nil {
		return routing.Response{}, err
	}
	return routing.Response{Payload: result}, nil
}
