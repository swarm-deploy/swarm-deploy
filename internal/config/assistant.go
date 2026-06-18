package config

import "github.com/artarts36/specw"

// AssistantToolName defines supported assistant MCP tool names.
type AssistantToolName string

const (
	// AssistantToolDeploySyncTrigger triggers synchronization.
	AssistantToolDeploySyncTrigger AssistantToolName = "deploy_sync_trigger"
	// AssistantToolHistoryEventList lists deployment/sync history events.
	AssistantToolHistoryEventList AssistantToolName = "history_event_list"
	// AssistantToolSwarmNodeList lists Docker Swarm nodes.
	AssistantToolSwarmNodeList AssistantToolName = "swarm_node_list"
	// AssistantToolDockerNetworkList lists Docker networks.
	AssistantToolDockerNetworkList AssistantToolName = "docker_network_list"
	// AssistantToolServiceWebRoutePing probes HTTP routes for services.
	AssistantToolServiceWebRoutePing AssistantToolName = "service_webroute_ping"
	// AssistantToolRegistryImageVersionGet resolves latest registry image version.
	AssistantToolRegistryImageVersionGet AssistantToolName = "registry_image_version_get"
	// AssistantToolDate returns current date/time for model reasoning.
	AssistantToolDate AssistantToolName = "date"
	// AssistantToolGitCommitList lists git commits.
	AssistantToolGitCommitList AssistantToolName = "git_commit_list"
	// AssistantToolGitCommitDiff shows git commit diff details.
	AssistantToolGitCommitDiff AssistantToolName = "git_commit_diff"
	// AssistantToolPromptInjectionReport reports prompt-injection attempts.
	AssistantToolPromptInjectionReport AssistantToolName = "assistant_prompt_injection_report"
)

// IsSupported reports whether assistant tool name is one of supported enum values.
func (t AssistantToolName) IsSupported() bool {
	switch t {
	case "",
		AssistantToolDeploySyncTrigger,
		AssistantToolHistoryEventList,
		AssistantToolSwarmNodeList,
		AssistantToolDockerNetworkList,
		AssistantToolServiceWebRoutePing,
		AssistantToolRegistryImageVersionGet,
		AssistantToolDate,
		AssistantToolGitCommitList,
		AssistantToolGitCommitDiff,
		AssistantToolPromptInjectionReport:
		return true
	default:
		return false
	}
}

// AssistantSpec configures AI assistant behavior.
type AssistantSpec struct {
	// Enabled toggles assistant API and UI visibility.
	Enabled bool `yaml:"enabled"`
	// Tools contains a list of allowed tool names. Empty means all built-in tools.
	Tools []AssistantToolName `yaml:"tools"`
	// SystemPrompt is an extra system instruction appended to built-in safety prompt.
	SystemPrompt string `yaml:"systemPrompt"`
	// Model contains LLM provider configuration.
	Model AssistantModelSpec `yaml:"model"`
	// Conversation contains assistant conversation storage settings.
	Conversation AssistantConversationSpec `yaml:"conversation"`
}

// AssistantModelSpec contains model-level settings.
type AssistantModelSpec struct {
	// Name is a model identifier used for chat completion.
	Name string `yaml:"name"`
	// EmbeddingName is a model identifier used for embeddings generation.
	EmbeddingName string `yaml:"embeddingName"`
	// OpenAI contains OpenAI-compatible endpoint and auth settings.
	OpenAI AssistantOpenAISpec `yaml:"openai"`
}

// AssistantOpenAISpec contains OpenAI-compatible transport settings.
type AssistantOpenAISpec struct {
	// BaseURL is an OpenAI-compatible API base URL.
	BaseURL string `yaml:"baseUrl"`
	// APIToken is a path to file containing API token.
	APIToken specw.File `yaml:"apiTokenPath"`
	// OrganizationID is an optional OpenAI organization identifier.
	OrganizationID string `yaml:"organizationId"`
	// Temperature is a model temperature value in [0, 2].
	Temperature string `yaml:"temperature"`
	// MaxTokens is a max generated token count.
	MaxTokens string `yaml:"maxTokens"`
}

// AssistantConversationSpec contains conversation settings.
type AssistantConversationSpec struct {
	// Storage configures conversation storage implementation.
	Storage AssistantConversationStorageSpec `yaml:"storage"`
}

// AssistantConversationStorageSpec contains storage configuration.
type AssistantConversationStorageSpec struct {
	// InMemory configures in-memory conversation storage.
	InMemory AssistantConversationInMemoryStorageSpec `yaml:"inMemory"`
}

// AssistantConversationInMemoryStorageSpec contains in-memory storage settings.
type AssistantConversationInMemoryStorageSpec struct {
	// TTL is a dialog retention duration for in-memory storage.
	TTL specw.Duration `yaml:"ttl"`
}
