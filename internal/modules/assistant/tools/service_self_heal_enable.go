package tools

import (
	"context"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/selfheal"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

type serviceSelfHealEnable struct {
	updater *selfheal.Service
}

type serviceSelfHealEnableRequest struct {
	Stack   string `json:"stack"`
	Service string `json:"service"`
	Source  string `json:"source"`
}

func (s *serviceSelfHealEnable) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        config.AssistantToolNameServiceSelfHealEnable,
		Writable:    true,
		Terminal:    true,
		Description: "Propose a GitOps service self-heal override on a Git branch.",
		Request:     serviceSelfHealEnableRequest{},
	}
}

func (s *serviceSelfHealEnable) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	input, err := convertRequestPayload[serviceSelfHealEnableRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	author := "swarm-deploy"
	if user, ok := security.UserFromContext(ctx); ok && strings.TrimSpace(user.Name) != "" {
		author = strings.Join(strings.Fields(user.Name), " ")
	}
	result, err := s.updater.Enable(ctx, input.Stack, input.Service, input.Source, author)
	if err != nil {
		return routing.Response{}, err
	}
	return routing.Response{Payload: result}, nil
}
