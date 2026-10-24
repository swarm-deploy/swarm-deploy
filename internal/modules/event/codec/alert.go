package codec

import (
	"encoding/json"
	"errors"
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
	case *events.OutboxDeliveryFailed:
		if e.ErrorCode == "" {
			return nil, true, errors.New("outbox delivery error code is required")
		}
		payload, err := encodeOutboxDelivery(e.OutboxDelivery, e.ErrorCode)
		return payload, true, err
	case *events.OutboxDeliveryRecovered:
		payload, err := encodeOutboxDelivery(e.OutboxDelivery, "")
		return payload, true, err
	case *events.OutboxDeliveryDiscarded:
		payload, err := encodeOutboxDelivery(e.OutboxDelivery, "")
		return payload, true, err
	case *events.GitPullFailed:
		payload, err := encodeGitPullFailed(e)
		return payload, true, err
	case *events.GitPullSucceeded:
		if err := validateRepositoryIdentifier(e.Repository); err != nil {
			return nil, true, err
		}
		payload, err := json.Marshal(gitPullSucceededPayload{Repository: e.Repository, TriggerReason: e.TriggerReason})
		return payload, true, err
	default:
		return nil, false, nil
	}
}

type outboxDeliveryPayload struct {
	EventID        string          `json:"event_id"`
	SubscriptionID string          `json:"subscription_id"`
	DestinationKey string          `json:"destination_key"`
	EventType      events.TypeName `json:"event_type"`
	Destination    string          `json:"destination"`
	Channel        string          `json:"channel"`
	ErrorCode      string          `json:"error_code,omitempty"`
}

func encodeOutboxDelivery(delivery events.OutboxDelivery, errorCode string) ([]byte, error) {
	payload := outboxDeliveryPayload{
		EventID: delivery.EventID, SubscriptionID: delivery.SubscriptionID, EventType: delivery.EventType,
		DestinationKey: delivery.DestinationKey, Destination: delivery.Destination,
		Channel: delivery.Channel, ErrorCode: errorCode,
	}
	if err := validateOutboxDeliveryPayload(payload, errorCode != ""); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func validateOutboxDeliveryPayload(payload outboxDeliveryPayload, requireError bool) error {
	if payload.EventID == "" || payload.SubscriptionID == "" || payload.DestinationKey == "" || payload.EventType == "" ||
		payload.Destination == "" || payload.Channel == "" {
		return errors.New("outbox delivery metadata is incomplete")
	}
	if requireError {
		switch payload.ErrorCode {
		case "handler_failed", "unsupported_or_invalid_payload", "subscription_missing":
		default:
			return errors.New("unsupported outbox delivery error code")
		}
	} else if payload.ErrorCode != "" {
		return errors.New("unexpected outbox delivery error code")
	}
	return nil
}

func (p outboxDeliveryPayload) delivery() events.OutboxDelivery {
	return events.OutboxDelivery{
		EventID: p.EventID, SubscriptionID: p.SubscriptionID, EventType: p.EventType,
		DestinationKey: p.DestinationKey, Destination: p.Destination, Channel: p.Channel,
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
	DeploymentID     string     `json:"deploymentId,omitempty"`
	ErrorCode        string     `json:"errorCode,omitempty"`
	Occurrences      uint64     `json:"occurrences,omitempty"`
	Duration         string     `json:"duration,omitempty"`
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
	return alertPayload{
		ID:               a.ID,
		Kind:             a.Kind,
		Title:            a.Title,
		Message:          a.Message,
		ResourceType:     a.ResourceType,
		ResourceID:       a.ResourceID,
		DeploymentID:     a.DeploymentID,
		ErrorCode:        a.ErrorCode,
		Occurrences:      a.Occurrences,
		Duration:         a.Duration,
		OpenedAt:         a.OpenedAt,
		ResolvedAt:       a.ResolvedAt,
		Resolution:       a.Resolution,
		ResolutionReason: a.ResolutionReason,
	}
}

func (p alertPayload) snapshot() events.AlertSnapshot {
	return events.AlertSnapshot{
		ID:               p.ID,
		Kind:             p.Kind,
		Title:            p.Title,
		Message:          p.Message,
		ResourceType:     p.ResourceType,
		ResourceID:       p.ResourceID,
		DeploymentID:     p.DeploymentID,
		ErrorCode:        p.ErrorCode,
		Occurrences:      p.Occurrences,
		Duration:         p.Duration,
		OpenedAt:         p.OpenedAt,
		ResolvedAt:       p.ResolvedAt,
		Resolution:       p.Resolution,
		ResolutionReason: p.ResolutionReason,
	}
}
