package assistant

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Module contains assistant services exposed to application entrypoints.
type Module struct {
	Service Assistant
}

// Container supplies dependencies required by the assistant module.
type Container interface {
	GetMetrics() *metrics.Group
	GetSwarm() *swarm.Swarm
	GetEventModule() *event.Module
	GetResourcesModule() *resources.Module
	GetGitOpsModule() *gitops.Module
	GetRecommendationsModule() *recommendations.Module
}

// InitModule initializes the assistant and its built-in tools.
func InitModule(_ context.Context, cfg *config.Config, cnt Container) (*Module, error) {
	if !cfg.Spec.Assistant.Enabled {
		return &Module{Service: &DisabledAssistant{}}, nil
	}

	temperature, err := cfg.Spec.Assistant.Model.OpenAI.ResolveTemperature()
	if err != nil {
		return nil, fmt.Errorf("resolve assistant temperature: %w", err)
	}

	imageVersionResolver, err := registry.NewImageVersionResolver()
	if err != nil {
		return nil, fmt.Errorf("build image version resolver: %w", err)
	}

	hostingProviders, err := githosting.NewProviderManager(cfg.Spec.Hostings)
	if err != nil {
		return nil, fmt.Errorf("build hosting providers: %w", err)
	}

	resourcesModule := cnt.GetResourcesModule()
	gitopsModule := cnt.GetGitOpsModule()
	eventModule := cnt.GetEventModule()

	toolExecutor := tools.NewExecutor(
		resourcesModule,
		gitopsModule,
		eventModule,
		cnt.GetSwarm(),
		cnt.GetRecommendationsModule().Store,
		imageVersionResolver,
		hostingProviders,
		cfg.Spec.Stacks,
		cnt.GetMetrics().MCP,
	)

	service, err := NewService(Config{
		Enabled:                 cfg.Spec.Assistant.Enabled,
		ModelName:               cfg.Spec.Assistant.Model.Name,
		EmbeddingModelName:      cfg.Spec.Assistant.Model.EmbeddingName,
		BaseURL:                 cfg.Spec.Assistant.Model.OpenAI.BaseURL,
		APIToken:                string(cfg.Spec.Assistant.Model.OpenAI.APIToken.Content),
		OrganizationID:          cfg.Spec.Assistant.Model.OpenAI.OrganizationID,
		Temperature:             temperature,
		MaxTokens:               cfg.Spec.Assistant.Model.OpenAI.MaxTokens,
		SystemPrompt:            cfg.Spec.Assistant.SystemPrompt,
		AllowedTools:            cfg.Spec.Assistant.Tools,
		ConversationInMemoryTTL: cfg.Spec.Assistant.Conversation.Storage.InMemory.TTL.Value,
		ConversationHistoryDir:  filepath.Join(cfg.Spec.DataDir, "assistant", "chats"),
	}, resourcesModule.ServiceStore, toolExecutor, eventModule.Dispatcher, cnt.GetMetrics().Assistant)
	if err != nil {
		return nil, err
	}

	return &Module{Service: service}, nil
}
