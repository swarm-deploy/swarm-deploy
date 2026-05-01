package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

func (g *graph) executeToolCall(ctx context.Context, modelToolCall modelToolCall) (string, error) {
	if !g.isToolAllowed(modelToolCall.Name) {
		return "", errors.New("tool is not allowed by assistant.tools configuration")
	}

	result, runErr := g.tools.Execute(ctx, routing.Request{
		ToolName: modelToolCall.Name,
		Payload:  modelToolCall.Arguments,
	})
	if runErr != nil {
		return "", runErr
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

func formatMCPToolCallError(toolName string, runErr error) string {
	return fmt.Sprintf(
		"MCP tool call failed: tool %q could not be executed. Error: %s",
		strings.TrimSpace(toolName),
		strings.TrimSpace(runErr.Error()),
	)
}
