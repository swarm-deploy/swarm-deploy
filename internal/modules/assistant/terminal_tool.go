package assistant

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/imagepin"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/selfheal"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
)

const (
	serviceRestartTriggerTool          = "service_restart_trigger"
	serviceReplicasSetTool             = "service_replicas_set"
	serviceSelfHealEnableTool          = "service_self_heal_enable"
	serviceSelfHealDisableTool         = "service_self_heal_disable"
	serviceImagePinTool                = "service_image_pin"
	deploySyncTriggerTool              = "deploy_sync_trigger"
	syncPauseGetTool                   = "sync_pause_get"
	syncPauseSetTool                   = "sync_pause_set"
	syncPauseResumeTool                = "sync_pause_resume"
	assistantPromptInjectionReportTool = "assistant_prompt_injection_report"
)

type terminalToolResult struct {
	text      string
	artifacts []conversation.Artifact
}

// finalizeTerminalToolResult formats the user-facing answer for a successful terminal tool result.
func finalizeTerminalToolResult(localizer *texts.Localizer, toolName, result string) (terminalToolResult, error) {
	switch toolName {
	case assistantPromptInjectionReportTool:
		return terminalText(localizer.Text("result.injection_rejected")), nil
	case deploySyncTriggerTool:
		return finalizeSyncToolResult(localizer, result)
	case syncPauseSetTool, syncPauseResumeTool:
		return finalizePauseToolResult(localizer, toolName, result)
	case serviceRestartTriggerTool, serviceReplicasSetTool:
		return finalizeServiceActionResult(localizer, toolName, result)
	case serviceSelfHealEnableTool, serviceSelfHealDisableTool:
		return finalizeSelfHeal(localizer, toolName, result)
	case serviceImagePinTool:
		return finalizeImagePin(localizer, result)
	default:
		return terminalToolResult{}, fmt.Errorf("terminal formatter is not configured for tool %q", toolName)
	}
}

func finalizeImagePin(localizer *texts.Localizer, result string) (terminalToolResult, error) {
	var update imagepin.Result
	if err := json.Unmarshal([]byte(result), &update); err != nil {
		return terminalToolResult{}, fmt.Errorf("decode image pin proposal: %w", err)
	}
	if update.AlreadyPinned {
		return terminalText(localizer.Text("result.image_pin_unchanged")), nil
	}
	if update.Branch == "" {
		return terminalToolResult{}, errors.New("image pin proposal must contain the branch name")
	}
	answer := localizer.Text("result.image_pin")
	if update.PullRequestFailed {
		answer += " " + localizer.Text("result.image_pin_pr_failed")
	}
	return terminalToolResult{
		text: answer,
		artifacts: gitPublicationArtifacts(
			update.Branch, update.BranchURL, update.PullRequestURL, update.PullRequestNumber,
		),
	}, nil
}

func finalizeSelfHeal(localizer *texts.Localizer, toolName, result string) (terminalToolResult, error) {
	var update selfheal.Result
	if err := json.Unmarshal([]byte(result), &update); err != nil {
		return terminalToolResult{}, fmt.Errorf("decode self-heal proposal: %w", err)
	}
	if update.AlreadyEnabled != "" {
		return terminalText(localizer.Text("result.self_heal_unchanged")), nil
	}
	if update.AlreadyDisabled != "" {
		return terminalText(localizer.Format("result.self_heal_disable_unchanged", map[string]any{
			"Source": localizer.Text("policy.source_" + update.AlreadyDisabled),
		})), nil
	}
	if update.Branch == "" {
		return terminalToolResult{}, errors.New("self-heal proposal must contain the branch name")
	}
	message := "result.self_heal"
	if toolName == serviceSelfHealDisableTool {
		message = "result.self_heal_disable"
	}
	answer := localizer.Text(message)
	if update.PullRequestFailed {
		answer += " " + localizer.Text("result.self_heal_pr_failed")
	}
	return terminalToolResult{
		text: answer,
		artifacts: gitPublicationArtifacts(
			update.Branch, update.BranchURL, update.PullRequestURL, update.PullRequestNumber,
		),
	}, nil
}

func gitPublicationArtifacts(branch, branchURL, pullRequestURL string, pullRequestNumber int) []conversation.Artifact {
	var artifacts []conversation.Artifact
	if pullRequestURL != "" {
		label := "Pull request"
		if pullRequestNumber > 0 {
			label = fmt.Sprintf("PR #%d", pullRequestNumber)
		}
		artifacts = append(artifacts, conversation.Artifact{
			Type: conversation.ArtifactTypePullRequest, Label: label, URL: pullRequestURL,
		})
	}
	return append(artifacts, conversation.Artifact{
		Type: conversation.ArtifactTypeGitBranch, Label: branch, URL: branchURL,
	})
}

func terminalText(text string) terminalToolResult { return terminalToolResult{text: text} }

func finalizeSyncToolResult(localizer *texts.Localizer, result string) (terminalToolResult, error) {
	var syncResult struct {
		Queued bool `json:"queued"`
	}
	if err := json.Unmarshal([]byte(result), &syncResult); err != nil {
		return terminalToolResult{}, fmt.Errorf("decode terminal sync result: %w", err)
	}
	if !syncResult.Queued {
		return terminalText(localizer.Text("result.sync_pending")), nil
	}
	return terminalText(localizer.Text("result.sync_queued")), nil
}

func finalizeServiceActionResult(localizer *texts.Localizer, toolName, result string) (terminalToolResult, error) {
	var actionResult tools.ServiceActionResult
	if err := json.Unmarshal([]byte(result), &actionResult); err != nil {
		return terminalToolResult{}, fmt.Errorf("decode terminal tool result: %w", err)
	}
	if strings.TrimSpace(actionResult.Stack) == "" {
		return terminalToolResult{}, errors.New("terminal tool result does not contain stack")
	}
	if strings.TrimSpace(actionResult.Service) == "" {
		return terminalToolResult{}, errors.New("terminal tool result does not contain service")
	}

	id := "result.restart"
	if toolName == serviceReplicasSetTool {
		id = "result.replicas"
	}
	answer := localizer.Format(id, map[string]any{
		"Stack": actionResult.Stack, "Service": actionResult.Service, "Count": actionResult.Replicas,
	})
	switch warning := strings.TrimSpace(actionResult.Warning); warning {
	case tools.ServiceActionWarningAuditEventNotPersisted:
		answer += " " + localizer.Text("result.audit_event_not_persisted")
	case "":
	default:
		answer += " " + localizer.Format("result.warning", map[string]any{"Warning": warning})
	}
	return terminalText(answer), nil
}

func finalizePauseToolResult(localizer *texts.Localizer, toolName, result string) (terminalToolResult, error) {
	var pauseResult struct {
		Scope   syncpause.Scope `json:"scope"`
		Stack   string          `json:"stack"`
		Service string          `json:"service"`
		Changed *bool           `json:"changed"`
	}
	if err := json.Unmarshal([]byte(result), &pauseResult); err != nil {
		return terminalToolResult{}, fmt.Errorf("decode Pause Sync result: %w", err)
	}
	if pauseResult.Changed != nil && !*pauseResult.Changed {
		return terminalText(localizer.Text("result.pause_unchanged")), nil
	}
	data := map[string]any{"Stack": pauseResult.Stack, "Service": pauseResult.Service}
	scope := string(syncpause.ScopeStack)
	if pauseResult.Scope == syncpause.ScopeService {
		scope = string(syncpause.ScopeService)
	}
	if toolName == syncPauseSetTool {
		return terminalText(localizer.Format("result.pause_"+scope+"_set", data)), nil
	}
	return terminalText(localizer.Format("result.pause_"+scope+"_removed", data)), nil
}
