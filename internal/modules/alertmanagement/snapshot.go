package alertmanagement

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

func alertSnapshot(alert model.Alert) events.AlertSnapshot {
	snapshot := events.AlertSnapshot{ID: alert.ID, Kind: string(alert.Kind), Title: alert.Title,
		Message: alert.Message, ResourceType: string(alert.ResourceType), ResourceID: alert.ResourceID,
		DeploymentID: alert.DeploymentID, ErrorCode: alert.ErrorCode, Occurrences: alert.Occurrences,
		OpenedAt: alert.OpenedAt, ResolvedAt: alert.ResolvedAt}
	if alert.Kind == model.AlertKindGitRepositoryUnavailable && alert.ResolvedAt != nil {
		duration := alert.ResolvedAt.Sub(alert.OpenedAt)
		if duration < 0 {
			duration = 0
		}
		snapshot.Duration = duration.Round(time.Second).String()
	}
	if alert.Resolution != nil {
		snapshot.Resolution = alert.Resolution.Message
		snapshot.ResolutionReason = string(alert.Resolution.Reason)
	}
	return snapshot
}
