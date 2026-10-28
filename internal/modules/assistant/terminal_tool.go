package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
)

const (
	serviceRestartTriggerTool          = "service_restart_trigger"
	serviceReplicasSetTool             = "service_replicas_set"
	deploySyncTriggerTool              = "deploy_sync_trigger"
	assistantPromptInjectionReportTool = "assistant_prompt_injection_report"
	promptInjectionRejectedResponse    = "Request was rejected by prompt injection protection."
)

// ToolBehavior describes built-in assistant behavior that is not part of the assistant tool contract.
type ToolBehavior struct {
	// Terminal indicates that a successful single result can finish without another model completion.
	Terminal bool
}

var toolBehaviors = map[string]ToolBehavior{
	serviceRestartTriggerTool:          {Terminal: true},
	serviceReplicasSetTool:             {Terminal: true},
	deploySyncTriggerTool:              {Terminal: true},
	assistantPromptInjectionReportTool: {Terminal: true},
}

func isMutatingTool(toolName string) bool {
	return toolName == serviceRestartTriggerTool ||
		toolName == serviceReplicasSetTool ||
		toolName == deploySyncTriggerTool
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
	if toolName == deploySyncTriggerTool {
		var syncResult struct {
			Queued bool `json:"queued"`
		}
		if err := json.Unmarshal([]byte(result), &syncResult); err != nil {
			return "", fmt.Errorf("decode terminal sync result: %w", err)
		}
		if !syncResult.Queued {
			return "Synchronization request was not queued because another synchronization is already pending.", nil
		}
		return "Synchronization request queued successfully.", nil
	}

	var actionResult tools.ServiceActionResult
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
		return appendActionWarning(fmt.Sprintf(
			"Service restart completed successfully: stack %q, service %q, current replicas: %d.",
			actionResult.Stack,
			actionResult.Service,
			actionResult.Replicas,
		), actionResult.Warning), nil
	case serviceReplicasSetTool:
		return appendActionWarning(fmt.Sprintf(
			"Service replicas updated successfully: stack %q, service %q, replicas set to %d.",
			actionResult.Stack,
			actionResult.Service,
			actionResult.Replicas,
		), actionResult.Warning), nil
	default:
		return "", fmt.Errorf("terminal formatter is not configured for tool %q", toolName)
	}
}

func appendActionWarning(answer, warning string) string {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return answer
	}
	return answer + " Warning: " + warning
}
