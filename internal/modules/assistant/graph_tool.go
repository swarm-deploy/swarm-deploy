package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/llm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

func (g *graph) executeToolCall(
	ctx context.Context,
	toolCall llm.ToolCall,
	effectiveToolSet map[string]struct{},
) (string, error) {
	definition, declared := g.toolDefinition(toolCall.Name)
	if !declared {
		return "", errors.New("tool is not available to the assistant")
	}
	if definition.Writable {
		return "", errors.New("mutating tools require deterministic confirmation")
	}
	if _, ok := effectiveToolSet[toolCall.Name]; !ok {
		return "", errors.New("tool is not allowed by the selected assistant capabilities")
	}

	result, runErr := g.tools.Execute(ctx, routing.Request{
		ToolName:   toolCall.Name,
		ToolCallID: toolCall.ID,
		Payload:    toolCall.Arguments,
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
		if definition.Writable {
			continue
		}
		if _, ok := effectiveToolSet[string(definition.Name)]; ok {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

// Guidance without live evidence only needs the built-in security report.
func (g *graph) responseToolSet(route Route, capabilities []Capability) map[string]struct{} {
	if route == RouteGeneral || (route == RoutePlatform && len(capabilities) == 0) {
		return map[string]struct{}{assistantPromptInjectionReportTool: {}}
	}
	return g.effectiveToolSet(capabilities)
}

// toolDefinition returns the executor-declared definition, including mutation and terminal behavior.
func (g *graph) toolDefinition(toolName string) (routing.ToolDefinition, bool) {
	for _, definition := range g.tools.Definitions() {
		if string(definition.Name) == toolName {
			return definition, true
		}
	}
	return routing.ToolDefinition{}, false
}

// isTerminalToolResultCandidate reports whether one successful terminal tool result can finish the request.
func (g *graph) isTerminalToolResultCandidate(results []toolExecutionResult) bool {
	if len(results) != 1 || !results[0].success {
		return false
	}
	definition, ok := g.toolDefinition(results[0].toolName)
	return ok && definition.Terminal
}

func (g *graph) effectiveToolSet(capabilities []Capability) map[string]struct{} {
	return capabilityToolSet(g.allowedToolSet, capabilities)
}

// capabilityToolSet returns capability tools permitted by assistant.tools plus always-available built-ins.
func capabilityToolSet(allowedToolSet map[string]struct{}, capabilities []Capability) map[string]struct{} {
	toolSet := make(map[string]struct{})
	for _, capability := range capabilities {
		profile, ok := capabilityProfile(capability)
		if !ok {
			continue
		}
		for _, toolName := range profile.Tools {
			if len(allowedToolSet) > 0 {
				if _, allowed := allowedToolSet[toolName]; !allowed {
					continue
				}
			}
			toolSet[toolName] = struct{}{}
		}
	}
	for _, toolName := range assistantUtilityTools {
		if len(allowedToolSet) > 0 {
			if _, ok := allowedToolSet[toolName]; !ok {
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

func capabilitiesNeedServiceContext(capabilities []Capability) bool {
	for _, capability := range capabilities {
		profile, ok := capabilityProfile(capability)
		if ok && profile.ServiceContext {
			return true
		}
	}
	return false
}

func toolCallFailure(ctx context.Context, toolName string, runErr error) string {
	if !isConfigurationTool(toolName) {
		slog.ErrorContext(ctx, "[graph] failed to run mcp tool",
			slog.String("tool.name", toolName),
			slog.Any("err", runErr),
		)
	}
	return formatToolCallError(toolName, runErr)
}

func formatToolCallError(toolName string, runErr error) string {
	return fmt.Sprintf(
		"tool call failed: tool %q could not be executed. Error: %s",
		strings.TrimSpace(toolName),
		strings.TrimSpace(runErr.Error()),
	)
}

// Preserve the call ID needed to send an error result, without echoing invalid arguments.
func toolCallsForModelHistory(calls []llm.ToolCall) []llm.ToolCall {
	history := slices.Clone(calls)
	for i, call := range history {
		if isConfigurationTool(call.Name) {
			if _, err := tools.ParseConfigGetSection(call.Arguments); err != nil {
				history[i].Arguments = "{}"
			}
		}
	}
	return history
}

const configurationToolName = string(config.AssistantToolNameSwarmDeployConfigGet)

func isConfigurationTool(name string) bool {
	return strings.TrimSpace(name) == configurationToolName
}
