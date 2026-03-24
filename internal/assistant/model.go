package assistant

type modelMessage struct {
	Role       string
	Content    string
	ToolCallID string
	Name       string
	ToolCalls  []modelToolCall
}

type modelToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type modelRequest struct {
	Model       string
	Temperature float64
	MaxTokens   int
	Messages    []modelMessage
	Tools       []ToolDefinition
}

type modelResponse struct {
	Content   string
	ToolCalls []modelToolCall
}
