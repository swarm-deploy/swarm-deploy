package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mcpTools "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/tools"
)

const (
	serviceRestartTriggerTool          = "service_restart_trigger"
	serviceReplicasSetTool             = "service_replicas_set"
	assistantPromptInjectionReportTool = "assistant_prompt_injection_report"
	promptInjectionRejectedResponse    = "Request was rejected by prompt injection protection."
)

// ToolBehavior describes built-in assistant behavior that is not part of the external MCP contract.
type ToolBehavior struct {
	// Terminal indicates that a successful single result can finish without another model completion.
	Terminal bool
}

var toolBehaviors = map[string]ToolBehavior{
	serviceRestartTriggerTool:          {Terminal: true},
	serviceReplicasSetTool:             {Terminal: true},
	assistantPromptInjectionReportTool: {Terminal: true},
}

func isTerminalTool(toolName string) bool {
	return toolBehaviors[toolName].Terminal
}

func isTerminalToolResultCandidate(results []toolExecutionResult) bool {
	return len(results) == 1 && results[0].success && isTerminalTool(results[0].toolName)
}

func finalizeTerminalToolResult(toolName, result string) (string, error) {
	if !isTerminalTool(toolName) {
		return "", fmt.Errorf("tool %q is not terminal", toolName)
	}
	if toolName == assistantPromptInjectionReportTool {
		return promptInjectionRejectedResponse, nil
	}

	var actionResult mcpTools.ServiceActionResult
	if err := json.Unmarshal([]byte(result), &actionResult); err != nil {
		return "", fmt.Errorf("decode terminal tool result: %w", err)
	}
	if strings.TrimSpace(actionResult.Stack) == "" {
		return "", errors.New("terminal tool result does not contain stack")
	}
	if strings.TrimSpace(actionResult.Service) == "" {
		return "", errors.New("terminal tool result does not contain service")
	}

	switch toolName {
	case serviceRestartTriggerTool:
		return fmt.Sprintf(
			"Service restart completed successfully: stack %q, service %q, current replicas: %d.",
			actionResult.Stack,
			actionResult.Service,
			actionResult.Replicas,
		), nil
	case serviceReplicasSetTool:
		return fmt.Sprintf(
			"Service replicas updated successfully: stack %q, service %q, replicas set to %d.",
			actionResult.Stack,
			actionResult.Service,
			actionResult.Replicas,
		), nil
	default:
		return "", fmt.Errorf("terminal formatter is not configured for tool %q", toolName)
	}
}
