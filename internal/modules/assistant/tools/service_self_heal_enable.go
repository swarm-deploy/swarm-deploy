package tools

import (
	"context"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
)

type serviceSelfHealEnable struct {
	enabler *usecase.ServiceSelfHealEnabler
}

func (s *serviceSelfHealEnable) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        config.AssistantToolNameServiceSelfHealEnable,
		Writable:    true,
		Terminal:    true,
		Description: "Propose a GitOps service self-heal override on a Git branch.",
		Request:     usecase.ServiceSelfHealPlan{},
	}
}

func (s *serviceSelfHealEnable) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	plan, err := convertRequestPayload[usecase.ServiceSelfHealPlan](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	author := "swarm-deploy"
	if user, ok := security.UserFromContext(ctx); ok && strings.TrimSpace(user.Name) != "" {
		author = strings.Join(strings.Fields(user.Name), " ")
	}
	result, err := s.enabler.Publish(ctx, plan, author)
	if err != nil {
		return routing.Response{}, err
	}
	return routing.Response{Payload: result}, nil
}
