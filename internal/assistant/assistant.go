package assistant

import "context"

type Assistant interface {
	Chat(ctx context.Context, request ChatRequest) ChatResponse
}

type DisabledAssistant struct{}

func (*DisabledAssistant) Chat(_ context.Context, req ChatRequest) ChatResponse {
	return ChatResponse{
		Status:         StatusDisabled,
		ConversationID: req.ConversationID,
		RequestID:      req.RequestID,
		ErrorMessage:   "assistant is disabled in configuration",
	}
}
