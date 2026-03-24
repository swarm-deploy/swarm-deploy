package config

import "github.com/artarts36/specw"

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
	// Name is a model identifier used for chat and embeddings.
	Name string `yaml:"name"`
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
