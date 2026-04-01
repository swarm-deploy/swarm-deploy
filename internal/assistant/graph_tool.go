package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

type toolFailedError struct {
	Err error
}

func (e *toolFailedError) Error() string {
	return e.Err.Error()
}

func (g *graph) executeToolCall(ctx context.Context, modelToolCall modelToolCall) (string, error) {
	if !g.isToolAllowed(modelToolCall.Name) {
		return "", errors.New("tool is not allowed by assistant.tools configuration")
	}

	arguments, decodeErr := decodeToolArguments(modelToolCall.Arguments)
	if decodeErr != nil {
		return "", fmt.Errorf("decode tool arguments: %w", decodeErr)
	}

	result, runErr := g.tools.Execute(ctx, modelToolCall.Name, arguments)
	if runErr != nil {
		return "", &toolFailedError{
			Err: runErr,
		}
	}

	return result, nil
}

func (g *graph) allowedToolDefinitions() []routing.ToolDefinition {
	definitions := g.tools.Definitions()
	if len(g.allowedToolSet) == 0 {
		return definitions
	}

	filtered := make([]routing.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if g.isToolAllowed(definition.Name) {
			filtered = append(filtered, definition)
		}
	}

	return filtered
}

func (g *graph) allowedToolNames() []string {
	definitions := g.allowedToolDefinitions()
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}

	return names
}

func (g *graph) isToolAllowed(toolName string) bool {
	if len(g.allowedToolSet) == 0 {
		return true
	}

	_, ok := g.allowedToolSet[toolName]
	return ok
}

func decodeToolArguments(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("decode tool arguments: %w", err)
	}
	if decoded == nil {
		return map[string]any{}, nil
	}

	return decoded, nil
}

func formatMCPToolCallError(toolName string, runErr error) string {
	return fmt.Sprintf(
		"MCP tool call failed: tool %q could not be executed. Error: %s",
		strings.TrimSpace(toolName),
		strings.TrimSpace(runErr.Error()),
	)
}
