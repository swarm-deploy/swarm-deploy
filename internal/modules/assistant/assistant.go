package assistant

import "context"

// Assistant provides chat execution and persisted chat history.
type Assistant interface {
	// Chat starts or polls an assistant request.
	Chat(ctx context.Context, request ChatRequest) ChatResponse
	// ListChats returns persisted chats ordered by latest activity.
	ListChats(ctx context.Context) ([]ChatSummary, error)
	// GetChat returns one persisted chat with messages.
	GetChat(ctx context.Context, id string) (ChatHistory, bool, error)
}

// DisabledAssistant is used when assistant support is disabled.
type DisabledAssistant struct{}

// Chat returns a disabled response.
func (*DisabledAssistant) Chat(_ context.Context, req ChatRequest) ChatResponse {
	return ChatResponse{
		Status:         StatusDisabled,
		ConversationID: req.ConversationID,
		RequestID:      req.RequestID,
		ErrorMessage:   "assistant is disabled in configuration",
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
