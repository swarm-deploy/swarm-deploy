package modelstore

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
)

// alertPayload contains only fields that are not stored in indexed SQL columns.
type alertPayload struct {
	Title         string                 `json:"title"`
	Message       string                 `json:"message"`
	FailureStage  string                 `json:"failureStage,omitempty"`
	ErrorCode     string                 `json:"errorCode,omitempty"`
	Commit        string                 `json:"commit,omitempty"`
	Occurrences   uint64                 `json:"occurrences"`
	OpenEventID   string                 `json:"openEventId"`
	LatestEventID string                 `json:"latestEventId"`
	Resolution    *model.AlertResolution `json:"resolution,omitempty"`
}

func payloadFromAlert(alert model.Alert) alertPayload {
	return alertPayload{
		Title:         alert.Title,
		Message:       alert.Message,
		FailureStage:  alert.FailureStage,
		ErrorCode:     alert.ErrorCode,
		Commit:        alert.Commit,
		Occurrences:   alert.Occurrences,
		OpenEventID:   alert.OpenEventID,
		LatestEventID: alert.LatestEventID,
		Resolution:    alert.Resolution,
	}
}

// alertScanner supports both sql.Row and sql.Rows without coupling the read model to a query.
type alertScanner interface {
	Scan(...any) error
}

func scanAlert(row alertScanner) (model.Alert, error) {
	var (
		alert        model.Alert
		deploymentID sql.NullString
		openedAt     int64
		updatedAt    int64
		resolvedAt   sql.NullInt64
		raw          []byte
		payload      alertPayload
	)
	if err := row.Scan(&alert.ID, &alert.Fingerprint, &alert.Kind,
		&alert.ResourceType, &alert.ResourceID, &deploymentID, &alert.Status,
		&openedAt, &updatedAt, &resolvedAt, &raw); err != nil {
		return model.Alert{}, err
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return model.Alert{}, err
	}
	alert.DeploymentID = deploymentID.String
	alert.OpenedAt = time.Unix(0, openedAt).UTC()
	alert.UpdatedAt = time.Unix(0, updatedAt).UTC()
	if resolvedAt.Valid {
		at := time.Unix(0, resolvedAt.Int64).UTC()
		alert.ResolvedAt = &at
	}
	alert.Title = payload.Title
	alert.Message = payload.Message
	alert.FailureStage = payload.FailureStage
	alert.ErrorCode = payload.ErrorCode
	alert.Commit = payload.Commit
	alert.Occurrences = payload.Occurrences
	alert.OpenEventID = payload.OpenEventID
	alert.LatestEventID = payload.LatestEventID
	alert.Resolution = payload.Resolution
	return alert, nil
}
