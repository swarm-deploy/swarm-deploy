package assistant

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
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

// ChatMessage is a user-visible persisted chat message.
type ChatMessage struct {
	// Role is a participant role.
	Role string `json:"role"`
	// Content is the message text.
	Content string `json:"content"`
}

// ChatSummary describes a persisted assistant chat without messages.
type ChatSummary struct {
	// ID identifies the chat.
	ID string `json:"id"`
	// Title is derived from the first user message.
	Title string `json:"title"`
	// CreatedAt is the chat creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp of the latest message.
	UpdatedAt time.Time `json:"updated_at"`
}

// ChatHistory contains a persisted assistant chat and its messages.
type ChatHistory struct {
	// ID identifies the chat.
	ID string `json:"id"`
	// Title is derived from the first user message.
	Title string `json:"title"`
	// CreatedAt is the chat creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp of the latest message.
	UpdatedAt time.Time `json:"updated_at"`
	// Messages contains the full persisted user-visible history.
	Messages []ChatMessage `json:"messages"`
}

// Config contains runtime assistant settings.
type Config struct {
	// Enabled toggles assistant execution.
	Enabled bool
	// ModelName is the model identifier used for chat completion.
	ModelName string
	// EmbeddingModelName is the model identifier used for embeddings generation.
	EmbeddingModelName string
	// BaseURL is an OpenAI-compatible API base URL.
	BaseURL string
	// APIToken is an OpenAI-compatible bearer token.
	APIToken string `json:"-"`
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
	// ConversationInMemoryTTL is a retention time for the in-memory conversation context cache.
	ConversationInMemoryTTL time.Duration
	// ConversationHistoryDir is a directory for persisted assistant chat history.
	ConversationHistoryDir string
}

// ServiceStore reads current service metadata used by RAG retrieval.
type ServiceStore interface {
	// List returns collected service metadata records.
	List() []service.Info
}

// ToolExecutor executes assistant tools.
type ToolExecutor interface {
	// Definitions returns all available tool definitions.
	Definitions() []routing.ToolDefinition
	// Execute runs a tool by name with decoded JSON arguments.
	Execute(ctx context.Context, req routing.Request) (string, error)
}
