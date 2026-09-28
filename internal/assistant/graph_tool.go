package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

func (g *graph) executeToolCall(
	ctx context.Context,
	modelToolCall modelToolCall,
	effectiveToolSet map[string]struct{},
) (string, error) {
	if _, ok := effectiveToolSet[modelToolCall.Name]; !ok {
		return "", errors.New("tool is not allowed by the selected assistant route")
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

func (g *graph) effectiveToolDefinitions(effectiveToolSet map[string]struct{}) []routing.ToolDefinition {
	definitions := g.tools.Definitions()
	filtered := make([]routing.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if _, ok := effectiveToolSet[string(definition.Name)]; ok {
			filtered = append(filtered, definition)
		}
	}

	return filtered
}

func (g *graph) effectiveToolSet(profile RouteProfile) map[string]struct{} {
	toolSet := make(map[string]struct{}, len(profile.Tools)+len(assistantUtilityTools)+len(assistantSecurityTools))
	for _, toolName := range profile.Tools {
		if len(g.allowedToolSet) > 0 {
			if _, ok := g.allowedToolSet[toolName]; !ok {
				continue
			}
		}
		toolSet[toolName] = struct{}{}
	}
	for _, toolName := range assistantUtilityTools {
		if len(g.allowedToolSet) > 0 {
			if _, ok := g.allowedToolSet[toolName]; !ok {
				continue
			}
		}
		toolSet[toolName] = struct{}{}
	}
	for _, toolName := range assistantSecurityTools {
		toolSet[toolName] = struct{}{}
	}

	return toolSet
}

func formatMCPToolCallError(toolName string, runErr error) string {
	return fmt.Sprintf(
		"MCP tool call failed: tool %q could not be executed. Error: %s",
		strings.TrimSpace(toolName),
		strings.TrimSpace(runErr.Error()),
	)
}
