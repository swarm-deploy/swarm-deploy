package handlers

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
)

const (
	latestDeploymentSuggestionLimit = 100
	maxAssistantSuggestions         = 4
)

// GetAssistantSuggestions returns best-effort prompts for an empty assistant chat.
func (h *handler) GetAssistantSuggestions(ctx context.Context) (*generated.AssistantSuggestionsResponse, error) {
	suggestions := make([]generated.AssistantSuggestion, 0, maxAssistantSuggestions)

	serviceSuggestion, ok, err := h.latestServiceSuggestion(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to retrieve assistant deployment suggestion", slog.Any("error", err))
	} else if ok {
		suggestions = append(suggestions, serviceSuggestion)
	}

	alertSuggestion, ok, err := h.latestAlertSuggestion(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to retrieve assistant alert suggestion", slog.Any("error", err))
	} else if ok {
		suggestions = append(suggestions, alertSuggestion)
	}

	suggestions = append(suggestions,
		generated.AssistantSuggestion{
			ID:     "cluster-state",
			Title:  h.assistantTexts.Text("suggestions.cluster_state.title"),
			Prompt: h.assistantTexts.Text("suggestions.cluster_state.prompt"),
		},
		generated.AssistantSuggestion{
			ID:     "today-deployments",
			Title:  h.assistantTexts.Text("suggestions.today_deployments.title"),
			Prompt: h.assistantTexts.Text("suggestions.today_deployments.prompt"),
		},
	)
	return &generated.AssistantSuggestionsResponse{Suggestions: suggestions}, nil
}

func (h *handler) latestServiceSuggestion(
	ctx context.Context,
) (generated.AssistantSuggestion, bool, error) {
	page, err := h.deployments.List(ctx, deploymentmodel.ListFilter{
		Status: deploymentmodel.Succeeded,
		Limit:  latestDeploymentSuggestionLimit,
	})
	if err != nil {
		return generated.AssistantSuggestion{}, false, err
	}

	for _, summary := range page.Deployments {
		if summary.Resources.Services == 0 {
			continue
		}

		deployed, getErr := h.deployments.Get(ctx, summary.ID)
		if getErr != nil {
			return generated.AssistantSuggestion{}, false, getErr
		}
		changedServices := changedServiceNames(deployed.Changes)
		if len(changedServices) == 0 {
			continue
		}

		desired, desiredErr := h.deployments.Desired(ctx, summary.ID)
		if desiredErr != nil {
			return generated.AssistantSuggestion{}, false, desiredErr
		}
		for _, serviceName := range changedServices {
			if !desiredHasService(desired.Compose.Services, serviceName) {
				continue
			}
			stackName := singleLine(summary.Stack)
			serviceName = singleLine(serviceName)
			if stackName == "" || serviceName == "" {
				continue
			}
			data := map[string]any{"Service": serviceName, "Stack": stackName}
			return generated.AssistantSuggestion{
				ID:     "latest-service-logs",
				Title:  h.assistantTexts.Format("suggestions.service_logs.title", data),
				Prompt: h.assistantTexts.Format("suggestions.service_logs.prompt", data),
			}, true, nil
		}
	}

	return generated.AssistantSuggestion{}, false, nil
}

func changedServiceNames(changes []deploymentmodel.Change) []string {
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for _, change := range changes {
		if change.ResourceType != "service" || change.ResourceName == "" {
			continue
		}
		if _, exists := seen[change.ResourceName]; exists {
			continue
		}
		seen[change.ResourceName] = struct{}{}
		names = append(names, change.ResourceName)
	}
	return names
}

func desiredHasService(services compose.Services, name string) bool {
	for _, service := range services {
		if service.Name == name {
			return true
		}
	}
	return false
}

func (h *handler) latestAlertSuggestion(
	ctx context.Context,
) (generated.AssistantSuggestion, bool, error) {
	alerts, err := h.alerts.List(ctx, modelstore.ListFilter{Limit: modelstore.MaxStoredAlerts})
	if err != nil {
		return generated.AssistantSuggestion{}, false, err
	}
	if len(alerts) == 0 {
		return generated.AssistantSuggestion{}, false, nil
	}

	latest := alerts[0]
	for _, alert := range alerts[1:] {
		if alert.OpenedAt.After(latest.OpenedAt) ||
			(alert.OpenedAt.Equal(latest.OpenedAt) && alert.ID < latest.ID) {
			latest = alert
		}
	}
	suggestion, ok := assistantAlertSuggestion(h.assistantTexts, latest)
	return suggestion, ok, nil
}

func assistantAlertSuggestion(
	localizer *texts.Localizer, alert alertmodel.Alert,
) (generated.AssistantSuggestion, bool) {
	alertID := singleLine(alert.ID)
	if alertID == "" || alert.OpenedAt.IsZero() {
		return generated.AssistantSuggestion{}, false
	}

	question := alertQuestion(localizer, alert)
	stack := ""
	if alert.ResourceType == alertmodel.ResourceTypeStack {
		stack = alert.ResourceID
	}
	fields := []struct{ id, value string }{
		{"id", alertID},
		{"type", string(alert.Kind)},
		{"status", string(alert.Status)},
		{"resource_type", string(alert.ResourceType)},
		{"resource_id", alert.ResourceID},
		{"stack", stack},
		{"deployment_id", alert.DeploymentID},
		{"failure_stage", alert.FailureStage},
		{"error_code", alert.ErrorCode},
		{"commit", alert.Commit},
		{"created_at", alert.OpenedAt.UTC().Format(time.RFC3339Nano)},
	}

	contextLines := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := singleLine(field.value); value != "" {
			line := localizer.Format("suggestions.alert.field."+field.id, map[string]any{"Value": value})
			contextLines = append(contextLines, line)
		}
	}
	prompt := localizer.Format("suggestions.alert.prompt", map[string]any{
		"Question": question, "Context": strings.Join(contextLines, "\n"),
	})
	return generated.AssistantSuggestion{ID: "latest-alert", Title: question, Prompt: prompt}, true
}

func alertQuestion(localizer *texts.Localizer, alert alertmodel.Alert) string {
	resourceID := singleLine(alert.ResourceID)
	data := map[string]any{"ResourceID": resourceID}
	switch alert.Kind {
	case alertmodel.AlertKindSyncFailed:
		if resourceID != "" {
			return localizer.Format("suggestions.alert.sync", data)
		}
		return localizer.Text("suggestions.alert.sync_no_resource")
	case alertmodel.AlertKindGitRepositoryUnavailable:
		return localizer.Text("suggestions.alert.git")
	case alertmodel.AlertKindNodeDisconnected:
		if resourceID != "" {
			return localizer.Format("suggestions.alert.node", data)
		}
		return localizer.Text("suggestions.alert.node_no_resource")
	case alertmodel.AlertKindServiceDrift:
		if resourceID != "" {
			return localizer.Format("suggestions.alert.drift", data)
		}
		return localizer.Text("suggestions.alert.drift_no_resource")
	case alertmodel.AlertKindMessengerUnavailable, alertmodel.AlertKindMessengerMisconfigured:
	}
	resourceType := singleLine(string(alert.ResourceType))
	if resourceType != "" && resourceID != "" {
		data["ResourceType"] = resourceType
		return localizer.Format("suggestions.alert.resource", data)
	}
	return localizer.Text("suggestions.alert.generic")
}

func singleLine(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value))
}
