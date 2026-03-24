package routing

import "context"

// ToolDefinition describes an executable tool visible to the model.
type ToolDefinition struct {
	// Name is a unique tool name.
	Name string
	// Description is a short usage description.
	Description string
	// ParametersJSONSchema is a JSON schema object for tool arguments.
	ParametersJSONSchema map[string]any
}

// Request describes an input payload for tool execution.
type Request struct {
	// Payload contains decoded JSON arguments keyed by argument name.
	Payload map[string]any
}

// Response describes a tool execution result payload.
type Response struct {
	// Payload contains a JSON-serializable response object.
	Payload any
}

// Tool describes one callable tool implementation.
type Tool interface {
	// Definition returns metadata visible to the model.
	Definition() ToolDefinition
	// Execute runs tool logic for the given request payload.
	Execute(ctx context.Context, request Request) (Response, error)
}
