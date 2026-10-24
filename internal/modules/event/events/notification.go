package events

import (
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// SendNotificationFailed is emitted when sending notification fails.
type SendNotificationFailed struct {
	// EventType is a source event type that triggered notification.
	EventType Type
	// Destination is a notification destination kind, for example telegram/custom.
	Destination string
	// Channel is a configured notifier name.
	Channel string
	// DestinationKey is a stable, non-reversible physical destination identity.
	DestinationKey string
	// SourceCausationID is opaque correlation metadata inherited from the delivered event.
	SourceCausationID string
	// Error is a delivery failure reason.
	Error error
}

// NotificationSent is emitted after an external notification is successfully delivered.
// It is an internal lifecycle fact and is not exposed through Event History or notification rules.
type NotificationSent struct {
	// EventType is the source event type that triggered notification.
	EventType Type
	// Destination is the notification destination kind, for example telegram/custom.
	Destination string
	// Channel is a safe human-readable configured channel name or deterministic fallback.
	Channel string
	// DestinationKey is a stable, non-reversible physical destination identity.
	DestinationKey string
}

// Type returns the internal delivery event type.
func (*NotificationSent) Type() Type { return TypeNotificationSent }

// Message returns a safe delivery summary.
func (n *NotificationSent) Message() string {
	return fmt.Sprintf("Notification sent to %s channel %s for %s", n.Destination, n.Channel, n.EventType)
}

// Details returns safe delivery metadata.
func (n *NotificationSent) Details() map[string]string {
	return map[string]string{
		"destination":     n.Destination,
		"channel":         n.Channel,
		"destination_key": n.DestinationKey,
		"event_type":      n.EventType.String(),
	}
}

func (n *SendNotificationFailed) Type() Type {
	return TypeSendNotificationFailed
}

func (n *SendNotificationFailed) Message() string {
	return fmt.Sprintf(
		"Send notification failed to %s channel %s for %s",
		n.Destination,
		n.Channel,
		n.EventType,
	)
}

func (n *SendNotificationFailed) Details() map[string]string {
	details := map[string]string{
		"destination":     n.Destination,
		"channel":         n.Channel,
		"destination_key": n.DestinationKey,
		"event_type":      n.EventType.String(),
	}
	if n.Error != nil {
		details[AttributeError] = faults.SafeDiagnostic(n.Error)
	}
	return details
}
