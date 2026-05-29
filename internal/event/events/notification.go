package events

import "fmt"

// SendNotificationFailed is emitted when sending notification fails.
type SendNotificationFailed struct {
	// EventType is a source event type that triggered notification.
	EventType Type
	// Destination is a notification destination kind, for example telegram/custom.
	Destination string
	// Channel is a configured notifier name.
	Channel string
	// Error is a delivery failure reason.
	Error error
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
		"destination": n.Destination,
		"channel":     n.Channel,
		"event_type":  n.EventType.String(),
	}
	if n.Error != nil {
		details["error"] = n.Error.Error()
	}
	return details
}
