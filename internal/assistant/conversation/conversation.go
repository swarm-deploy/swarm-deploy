package conversation

import "time"

// Turn describes one message in a conversation.
type Turn struct {
	// Role is a participant role ("user", "assistant", or "system").
	Role string
	// Content is a raw message text.
	Content string
}

// Conversation contains conversation messages and metadata.
type Conversation struct {
	// Turns keeps conversation messages in chronological order.
	Turns []Turn
	// LastMessageAt is a timestamp of the latest appended message.
	LastMessageAt time.Time
}

// Storage persists conversations.
type Storage interface {
	// Get returns conversation by conversation id.
	Get(id string) (Conversation, bool)
	// Append appends turns to conversation and updates LastMessageAt.
	Append(id string, turns ...Turn)
}
