package assistant

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
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

// ChatRequest holds input for starting or polling an assistant run.
type ChatRequest struct {
	// ConversationID identifies a multi-message conversation thread.
	ConversationID string
	// RequestID identifies a single assistant run used for polling.
	RequestID string
	// Message is a user message for a new run.
	Message string
	// CreatedBy is the authenticated username to use if the chat is first persisted by this run.
	CreatedBy string
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
	// AnswerSequence identifies the stored reply when a completed run has persistent history.
	AnswerSequence *int
	// ErrorMessage contains a user-safe error when run failed or was rejected.
	ErrorMessage string
	// PollAfterMS is a suggested delay before next poll request.
	PollAfterMS int
	// Activity contains safe user-visible execution progress for the current run.
	Activity []string
	// Selection contains structured reply options for completed runs.
	Selection *Selection
	// Artifacts contains structured outputs produced by a completed run.
	Artifacts []Artifact
}

// Artifact describes a durable output produced by an assistant operation.
type Artifact = conversation.Artifact

// ChatMessage is a user-visible persisted chat message.
type ChatMessage struct {
	// Sequence identifies this persisted turn within its chat.
	Sequence int `json:"sequence"`
	// Role is a participant role.
	Role string `json:"role"`
	// Content is the message text.
	Content string `json:"content"`
	// Activity contains persisted execution trace metadata for assistant messages.
	Activity []string `json:"activity,omitempty"`
	// Selection contains structured reply options for assistant messages.
	Selection *Selection `json:"selection,omitempty"`
	// Artifacts contains structured outputs produced by an assistant operation.
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Selection contains structured single-choice reply options.
type Selection = conversation.Selection

// SelectionOption describes one selectable reply value.
type SelectionOption = conversation.SelectionOption

// TokenUsage contains aggregate model token consumption for a chat.
type TokenUsage = conversation.TokenUsage

// UsageInterval is a UTC calendar grouping for assistant token consumption.
type UsageInterval = conversation.UsageInterval

// UsageBucket contains recorded token consumption for one calendar interval.
type UsageBucket = conversation.UsageBucket

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
	// Unread indicates that the latest persisted assistant reply is unread.
	Unread bool `json:"unread"`
	// Usage contains aggregate token consumption when reported by the model provider.
	Usage *TokenUsage `json:"token_usage,omitempty"`
	// PrimaryArtifact is the latest pull request, or the latest other artifact when no pull request exists.
	PrimaryArtifact *Artifact `json:"primary_artifact,omitempty"`
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
	// Unread indicates that the latest persisted assistant reply is unread.
	Unread bool `json:"unread"`
	// Usage contains aggregate token consumption when reported by the model provider.
	Usage *TokenUsage `json:"token_usage,omitempty"`
	// Messages contains the full persisted user-visible history.
	Messages []ChatMessage `json:"messages"`
	// ActiveRequest describes a reply that is still being generated, so clients can resume polling.
	ActiveRequest *ActiveChatRequest `json:"active_request,omitempty"`
}

// ActiveChatRequest describes an unfinished assistant request of a chat.
type ActiveChatRequest struct {
	// RequestID identifies the run to poll.
	RequestID string `json:"request_id"`
	// Activity contains safe user-visible execution progress of the run.
	Activity []string `json:"activity,omitempty"`
}

// Config contains runtime assistant settings.
type Config struct {
	// Localizer renders new assistant responses in the installation language.
	Localizer *texts.Localizer
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
	// ConversationHistory optionally supplies persistent chat storage; production uses SQLite.
	ConversationHistory conversation.HistoryStorage
	// SyncPauses optionally supplies GitOps Pause Sync state for pending operation target validation.
	// Pause Sync operations are unavailable when it is nil.
	SyncPauses SyncPauseTargetResolver
	// PrepareSelfHeal prepares a GitOps proposal for an exact catalog service when available.
	PrepareSelfHeal func(
		ctx context.Context, stack, service string, desiredEnabled bool,
	) (usecase.ServiceSelfHealPlan, error)
	// PrepareImagePin assesses the running task images against Git for an exact service.
	PrepareImagePin func(
		ctx context.Context, stack, service string,
	) (usecase.ServiceImagePinAssessment, error)
}

// ServiceStore reads current service metadata used by RAG retrieval.
type ServiceStore interface {
	// List returns collected service metadata records.
	ReadAll(ctx context.Context) ([]model.Info, error)
}

// ToolExecutor executes assistant tools.
type ToolExecutor interface {
	// Definitions returns all available tool definitions.
	Definitions() []routing.ToolDefinition
	// Execute runs a tool by name with decoded JSON arguments.
	Execute(ctx context.Context, req routing.Request) (string, error)
}
