package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

func (g *graph) executeToolCall(
	ctx context.Context,
	modelToolCall modelToolCall,
	effectiveToolSet map[string]struct{},
) (string, error) {
	if isMutatingTool(modelToolCall.Name) {
		return "", errors.New("mutating tools require deterministic confirmation")
	}
	if _, ok := effectiveToolSet[modelToolCall.Name]; !ok {
		return "", errors.New("tool is not allowed by the selected assistant capabilities")
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

// Check all requested writes before collecting targets or asking for confirmation.
func (g *graph) operationLimitation(result RouteResult) string {
	if result.UnsupportedAction {
		return "There is no matching executable operation for the requested change."
	}
	op := result.Operation
	if op == nil {
		return ""
	}
	required := []string{string(op.Type)}
	if op.Type == OperationSyncPauseSet && op.Replicas != nil {
		required = append(required, serviceReplicasSetTool)
	}
	definitions := g.tools.Definitions()
	for _, name := range required {
		registered := slices.ContainsFunc(definitions, func(def routing.ToolDefinition) bool {
			return string(def.Name) == name
		})
		if !registered {
			return "The requested operation is unavailable in this installation."
		}
		if _, allowed := g.allowedToolSet[name]; len(g.allowedToolSet) > 0 && !allowed {
			return fmt.Sprintf("The assistant.tools setting disables %s in this installation.", name)
		}
	}
	return ""
}

func (g *graph) effectiveToolDefinitions(effectiveToolSet map[string]struct{}) []routing.ToolDefinition {
	definitions := g.tools.Definitions()
	filtered := make([]routing.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if isMutatingTool(string(definition.Name)) {
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
	if route == RouteOutOfScope || (route == RoutePlatform && len(capabilities) == 0) {
		return map[string]struct{}{assistantPromptInjectionReportTool: {}}
	}
	return g.effectiveToolSet(capabilities)
}

func (g *graph) effectiveToolSet(capabilities []Capability) map[string]struct{} {
	toolSet := make(map[string]struct{})
	for _, capability := range capabilities {
		profile, ok := capabilityProfile(capability)
		if !ok {
			continue
		}
		for _, toolName := range profile.Tools {
			if len(g.allowedToolSet) > 0 {
				if _, allowed := g.allowedToolSet[toolName]; !allowed {
					continue
				}
			}
			toolSet[toolName] = struct{}{}
		}
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
func toolCallsForModelHistory(calls []modelToolCall) []modelToolCall {
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

func isConfigurationTool(name string) bool {
	return strings.TrimSpace(name) == configurationToolName
}
