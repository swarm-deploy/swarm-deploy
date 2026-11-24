package assistant

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Module contains assistant services exposed to application entrypoints.
type Module struct {
	Service Assistant
}

// Container supplies dependencies required by the assistant module.
type Container interface {
	// GetStorage returns the shared database.
	GetStorage() storage.Database
	GetMetrics() *metrics.Group
	GetSwarm() *swarm.Swarm
	// GetTaskLogs returns the shared task log parsing and filtering service.
	GetTaskLogs() *logparser.Service
	GetEventModule() *event.Module
	// GetConfigStore returns the shared runtime configuration store.
	GetConfigStore() *configstore.ConfigStore
	GetResourcesModule() *resources.Module
	// GetDeploymentsModule returns deployment persistence and execution infrastructure.
	GetDeploymentsModule() *deployments.Module
	GetGitOpsModule() *gitops.Module
	GetRecommendationsModule() *recommendations.Module
	// GetAlertManagementModule returns the initialized Alert Management module.
	GetAlertManagementModule() *alertmanagement.Module
}

// InitModule initializes the assistant and its built-in tools.
func InitModule(_ context.Context, cfg *config.Config, cnt Container, localizer *texts.Localizer) (*Module, error) {
	if !cfg.Spec.Assistant.Enabled {
		return &Module{Service: NewDisabledAssistant(localizer)}, nil
	}

	temperature, err := cfg.Spec.Assistant.Model.OpenAI.ResolveTemperature()
	if err != nil {
		return nil, fmt.Errorf("resolve assistant temperature: %w", err)
	}

	swarmManagers := cnt.GetSwarm()
	imageVersionResolver := registry.NewImageVersionResolver(swarmManagers.Images)

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
		cnt.GetDeploymentsModule().Store,
		cnt.GetAlertManagementModule(),
		eventModule,
		swarmManagers,
		cnt.GetTaskLogs(),
		cnt.GetRecommendationsModule().Store,
		imageVersionResolver,
		hostingProviders,
		cnt.GetConfigStore().Stacks(),
		cnt.GetMetrics().MCP,
		cfg.Spec,
	)

	service, err := NewService(Config{
		Localizer:               localizer,
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
		ConversationHistory:     conversation.NewSQLHistoryStorage(cnt.GetStorage()),
		SyncPauses:              gitopsModule.SyncPauses,
		PreviewSelfHeal:         gitopsModule.SelfHealProposals.Preview,
	}, resourcesModule.ServiceStore, toolExecutor, eventModule.Dispatcher, cnt.GetMetrics().Assistant)
	if err != nil {
		return nil, err
	}

	err = eventModule.Dispatcher.Subscribe(
		events.TypeNameServiceCatalogUpdated, "assistant-rag-index", dispatcher.External(service.RAGIndexSubscriber()),
	)
	if err != nil {
		return nil, fmt.Errorf("subscribe assistant RAG index: %w", err)
	}

	return &Module{Service: service}, nil
}
