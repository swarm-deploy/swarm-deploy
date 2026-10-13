package codec

import (
	"encoding/json"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

func encodeInternal(event events.Event) ([]byte, bool, error) {
	switch e := event.(type) {
	case *events.AlertOpened:
		payload, err := json.Marshal(alertOpenedPayload{Alert: alertPayloadFrom(e.Alert), Recipients: e.Recipients})
		return payload, true, err
	case *events.AlertResolved:
		payload, err := json.Marshal(alertResolvedPayload{Alert: alertPayloadFrom(e.Alert), Recipients: e.Recipients})
		return payload, true, err
	default:
		return nil, false, nil
	}
}

// alertPayload explicitly lists the only alert fields permitted in the Outbox.
type alertPayload struct {
	ID               string     `json:"id"`
	Kind             string     `json:"kind"`
	Title            string     `json:"title"`
	Message          string     `json:"message"`
	ResourceType     string     `json:"resourceType"`
	ResourceID       string     `json:"resourceId"`
	OpenedAt         time.Time  `json:"openedAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
	Resolution       string     `json:"resolution,omitempty"`
	ResolutionReason string     `json:"resolutionReason,omitempty"`
}

type alertOpenedPayload struct {
	Alert      alertPayload            `json:"alert"`
	Recipients []events.AlertRecipient `json:"recipients"`
}

type alertResolvedPayload struct {
	Alert      alertPayload            `json:"alert"`
	Recipients []events.AlertRecipient `json:"recipients"`
}

func alertPayloadFrom(a events.AlertSnapshot) alertPayload {
	return alertPayload{ID: a.ID, Kind: a.Kind, Title: a.Title, Message: a.Message,
		ResourceType: a.ResourceType, ResourceID: a.ResourceID, OpenedAt: a.OpenedAt,
		ResolvedAt: a.ResolvedAt, Resolution: a.Resolution, ResolutionReason: a.ResolutionReason}
}

func (p alertPayload) snapshot() events.AlertSnapshot {
	return events.AlertSnapshot{ID: p.ID, Kind: p.Kind, Title: p.Title, Message: p.Message,
		ResourceType: p.ResourceType, ResourceID: p.ResourceID, OpenedAt: p.OpenedAt,
		ResolvedAt: p.ResolvedAt, Resolution: p.Resolution, ResolutionReason: p.ResolutionReason}
}
