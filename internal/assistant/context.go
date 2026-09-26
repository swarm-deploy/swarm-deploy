package assistant

import "context"

type chatIDContextKey struct{}

// WithChatID stores an assistant chat identifier in context for tool correlation.
func WithChatID(ctx context.Context, chatID string) context.Context {
	return context.WithValue(ctx, chatIDContextKey{}, chatID)
}

// ChatIDFromContext returns the assistant chat identifier associated with the current run.
func ChatIDFromContext(ctx context.Context) (string, bool) {
	chatID, ok := ctx.Value(chatIDContextKey{}).(string)
	return chatID, ok && chatID != ""
}
