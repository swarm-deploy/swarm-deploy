package assistant

import (
	"context"
	"time"

	"github.com/artarts36/swarm-deploy/internal/service"
)

// Status is an assistant chat run state.
type Status string

const (
	// StatusInProgress means response generation is still running.
	StatusInProgress Status = "in_progress"
	// StatusCompleted means response generation has completed successfully.
	StatusCompleted Status = "completed"
	// StatusFailed means response generation has failed with an error.
	StatusFailed Status = "failed"
	// StatusRejected means request was rejected by safety guard.
	StatusRejected Status = "rejected"
	// StatusDisabled means assistant feature is disabled in configuration.
	StatusDisabled Status = "disabled"
)

// ToolCall describes a tool invocation made during assistant response generation.
type ToolCall struct {
	// Name is the tool name.
	Name string
	// Arguments is a raw JSON object with tool call arguments.
	Arguments string
	// Result is a text result returned by the tool.
	Result string
	// Error is a tool execution error message, if any.
	Error string
}

// ChatRequest is a chat API request payload for start/poll workflow.
type ChatRequest struct {
	// ConversationID identifies a multi-message conversation thread.
	ConversationID string
	// RequestID identifies a single assistant run used for polling.
	RequestID string
	// Message is a user message for a new run.
	Message string
	// WaitTimeoutMS is a server-side long-poll wait timeout in milliseconds.
	WaitTimeoutMS int
}

// ChatResponse is a chat API response payload.
type ChatResponse struct {
	// Status is a run state.
	Status Status
	// ConversationID identifies a conversation.
	ConversationID string
	// RequestID identifies a run.
	RequestID string
	// Answer is a final assistant answer for completed runs.
	Answer string
	// ErrorMessage contains a user-safe error when run failed or was rejected.
	ErrorMessage string
	// PollAfterMS is a suggested delay before next poll request.
	PollAfterMS int
}

// Config contains runtime assistant settings.
type Config struct {
	// Enabled toggles assistant execution.
	Enabled bool
	// ModelName is the model identifier used for chat and embeddings.
	ModelName string
	// BaseURL is an OpenAI-compatible API base URL.
	BaseURL string
	// APIToken is an OpenAI-compatible bearer token.
	APIToken string //nolint:gosec // Config only carries secret from file-based source and is not an API payload.
	// OrganizationID is an optional OpenAI organization identifier.
	OrganizationID string
	// Temperature controls model sampling temperature.
	Temperature float64
	// MaxTokens limits model output size.
	MaxTokens int
	// SystemPrompt appends project-specific guidance to the built-in safety prompt.
	SystemPrompt string
	// AllowedTools restricts available tool names. Empty means all tools.
	AllowedTools []string
	// ConversationInMemoryTTL is a retention time for in-memory conversations.
	ConversationInMemoryTTL time.Duration
}

// ServiceStore reads current service metadata used by RAG retrieval.
type ServiceStore interface {
	// List returns collected service metadata records.
	List() []service.Info
}

// ToolDefinition describes an executable tool visible to the model.
type ToolDefinition struct {
	// Name is a unique tool name.
	Name string
	// Description is a short usage description.
	Description string
	// ParametersJSONSchema is a JSON schema object for tool arguments.
	ParametersJSONSchema map[string]any
}

// ToolExecutor executes assistant tools.
type ToolExecutor interface {
	// Definitions returns all available tool definitions.
	Definitions() []ToolDefinition
	// Execute runs a tool by name with decoded JSON arguments.
	Execute(ctx context.Context, name string, arguments map[string]any) (string, error)
}
