package modelstore

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
)

// alertPayload contains only fields that are not stored in indexed SQL columns.
type alertPayload struct {
	Title               string                 `json:"title"`
	Message             string                 `json:"message"`
	FailureStage        string                 `json:"failureStage,omitempty"`
	ErrorCode           string                 `json:"errorCode,omitempty"`
	Commit              string                 `json:"commit,omitempty"`
	Occurrences         uint64                 `json:"occurrences"`
	OpenEventID         string                 `json:"openEventId"`
	LatestEventID       string                 `json:"latestEventId"`
	LatestEventSequence int64                  `json:"latestEventSequence,omitempty"`
	Resolution          *model.AlertResolution `json:"resolution,omitempty"`
}

func payloadFromAlert(alert model.Alert) alertPayload {
	return alertPayload{
		Title:               alert.Title,
		Message:             alert.Message,
		FailureStage:        alert.FailureStage,
		ErrorCode:           alert.ErrorCode,
		Commit:              alert.Commit,
		Occurrences:         alert.Occurrences,
		OpenEventID:         alert.OpenEventID,
		LatestEventID:       alert.LatestEventID,
		LatestEventSequence: alert.LatestEventSequence,
		Resolution:          alert.Resolution,
	}
}

// alertRow mirrors one row of the alerts table for sqlx struct scanning.
type alertRow struct {
	ID           string             `db:"id"`
	Fingerprint  string             `db:"fingerprint"`
	Kind         model.AlertKind    `db:"kind"`
	ResourceType model.ResourceType `db:"resource_type"`
	ResourceID   string             `db:"resource_id"`
	DeploymentID sql.NullString     `db:"deployment_id"`
	Status       model.AlertStatus  `db:"status"`
	OpenedAt     time.Time          `db:"opened_at"`
	UpdatedAt    time.Time          `db:"updated_at"`
	ResolvedAt   sql.NullTime       `db:"resolved_at"`
	Payload      []byte             `db:"payload"`
}

func (r alertRow) toAlert() (model.Alert, error) {
	var payload alertPayload
	if err := json.Unmarshal(r.Payload, &payload); err != nil {
		return model.Alert{}, err
	}
	alert := model.Alert{
		ID:                  r.ID,
		Fingerprint:         r.Fingerprint,
		Kind:                r.Kind,
		ResourceType:        r.ResourceType,
		ResourceID:          r.ResourceID,
		DeploymentID:        r.DeploymentID.String,
		Status:              r.Status,
		OpenedAt:            r.OpenedAt.UTC(),
		UpdatedAt:           r.UpdatedAt.UTC(),
		Title:               payload.Title,
		Message:             payload.Message,
		FailureStage:        payload.FailureStage,
		ErrorCode:           payload.ErrorCode,
		Commit:              payload.Commit,
		Occurrences:         payload.Occurrences,
		OpenEventID:         payload.OpenEventID,
		LatestEventID:       payload.LatestEventID,
		LatestEventSequence: payload.LatestEventSequence,
		Resolution:          payload.Resolution,
	}
	if r.ResolvedAt.Valid {
		at := r.ResolvedAt.Time.UTC()
		alert.ResolvedAt = &at
	}
	return alert, nil
}
