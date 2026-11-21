package llm

import (
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

// Message is a message sent to the chat completion provider.
type Message struct {
	// Role identifies the message author.
	Role string
	// Content is the message text.
	Content string
	// ToolCallID links a tool result to the corresponding assistant tool call.
	ToolCallID string
	// Name is the optional assistant name or the name of a legacy function.
	Name string
	// ToolCalls are the calls requested by an assistant message.
	ToolCalls []ToolCall
}

// ToolCall is a function call requested by the chat completion provider.
type ToolCall struct {
	// ID correlates the call with its result.
	ID string
	// Name identifies the function to call.
	Name string
	// Arguments contains the raw JSON arguments supplied by the provider.
	Arguments string
}

// Request contains the messages and settings for a chat completion.
type Request struct {
	// Model identifies the provider model.
	Model string
	// Temperature controls sampling.
	Temperature float64
	// MaxTokens limits the number of generated tokens.
	MaxTokens int
	// Messages contains the chat input in conversation order.
	Messages []Message
	// Tools defines the tools available to the provider for this request.
	Tools []routing.ToolDefinition
}

// Response contains the result of a chat completion.
type Response struct {
	// Content is the generated text.
	Content string
	// ToolCalls are the calls requested by the provider.
	ToolCalls []ToolCall
	// Usage is the token usage reported by the provider.
	Usage conversation.TokenUsage
}
