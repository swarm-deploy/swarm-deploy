package config

import "github.com/artarts36/specw"

// AssistantToolName identifies a built-in assistant tool.
type AssistantToolName string

const (
	AssistantToolNameHistoryEventList                   AssistantToolName = "history_event_list"
	AssistantToolNameDeploySyncTrigger                  AssistantToolName = "deploy_sync_trigger"
	AssistantToolNameSwarmNodeList                      AssistantToolName = "swarm_node_list"
	AssistantToolNameDockerNetworkList                  AssistantToolName = "docker_network_list"
	AssistantToolNameDockerPluginList                   AssistantToolName = "docker_plugin_list"
	AssistantToolNameDockerSecretList                   AssistantToolName = "docker_secret_list"
	AssistantToolNameServiceLogsGet                     AssistantToolName = "service_logs_get"
	AssistantToolNameServiceSpecGet                     AssistantToolName = "service_spec_get"
	AssistantToolNameDNSNameResolve                     AssistantToolName = "dns_name_resolve"
	AssistantToolNameServiceWebRoutePing                AssistantToolName = "service_webroute_ping"
	AssistantToolNameDependencyGraphGet                 AssistantToolName = "dependency_graph_get"
	AssistantToolNameRecommendationList                 AssistantToolName = "recommendation_list"
	AssistantToolNameServiceReplicasSet                 AssistantToolName = "service_replicas_set"
	AssistantToolNameServiceRestartTrigger              AssistantToolName = "service_restart_trigger"
	AssistantToolNameRegistryImageVersionGet            AssistantToolName = "registry_image_version_get"
	AssistantToolNameGitCommitList                      AssistantToolName = "git_commit_list"
	AssistantToolNameGitCommitDiff                      AssistantToolName = "git_commit_diff"
	AssistantToolNameExternalRepositoryReleaseLatestGet AssistantToolName = "external_repository_release_latest_get"
	AssistantToolNameDate                               AssistantToolName = "date"
	AssistantToolNameSelfMetricsList                    AssistantToolName = "self_metrics_list"
	AssistantToolNameAssistantPromptInjectionReport     AssistantToolName = "assistant_prompt_injection_report"
)

// AssistantSpec configures AI assistant behavior.
type AssistantSpec struct {
	// Enabled toggles assistant API and UI visibility.
	Enabled bool `yaml:"enabled"`
	// Tools contains a list of allowed tool names. Empty means all built-in tools.
	Tools []string `yaml:"tools"`
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
	MaxTokens int `yaml:"maxTokens"`
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
