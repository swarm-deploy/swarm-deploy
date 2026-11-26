package assistant

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
)

// Assistant provides chat execution and persisted chat history.
type Assistant interface {
	// Chat starts or polls an assistant request.
	Chat(ctx context.Context, request ChatRequest) ChatResponse
	// ListChats returns persisted chats ordered by latest activity.
	ListChats(ctx context.Context) ([]ChatSummary, error)
	// GetChat returns one persisted chat with messages.
	GetChat(ctx context.Context, id string) (ChatHistory, bool, error)
	// MarkChatRead clears unread if sequence identifies the latest assistant reply.
	MarkChatRead(ctx context.Context, id string, sequence int) error
	// GetUsage returns recorded token consumption across chats.
	GetUsage(ctx context.Context, from, to time.Time, interval UsageInterval) ([]UsageBucket, error)
}

// DisabledAssistant is used when assistant support is disabled.
type DisabledAssistant struct {
	texts *texts.Localizer
}

// NewDisabledAssistant creates the disabled implementation with localized responses.
func NewDisabledAssistant(localizer *texts.Localizer) *DisabledAssistant {
	return &DisabledAssistant{texts: localizer}
}

// Chat returns a disabled response.
func (a *DisabledAssistant) Chat(_ context.Context, req ChatRequest) ChatResponse {
	return ChatResponse{
		Status:         StatusDisabled,
		ConversationID: req.ConversationID,
		RequestID:      req.RequestID,
		ReadOnly:       req.ReadOnly,
		ErrorMessage:   a.texts.Text("chat.disabled"),
	}
}

// ListChats returns no chats for a disabled assistant.
func (*DisabledAssistant) ListChats(_ context.Context) ([]ChatSummary, error) {
	return []ChatSummary{}, nil
}

// GetChat returns no chat for a disabled assistant.
func (*DisabledAssistant) GetChat(_ context.Context, _ string) (ChatHistory, bool, error) {
	return ChatHistory{}, false, nil
}

// MarkChatRead does nothing when the assistant is disabled.
func (*DisabledAssistant) MarkChatRead(_ context.Context, _ string, _ int) error {
	return nil
}

// GetUsage returns no usage for a disabled assistant.
func (*DisabledAssistant) GetUsage(_ context.Context, _, _ time.Time, _ UsageInterval) ([]UsageBucket, error) {
	return []UsageBucket{}, nil
}
