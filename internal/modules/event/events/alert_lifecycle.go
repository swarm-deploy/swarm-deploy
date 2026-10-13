package events

import "time"

// AlertSnapshot is the safe, typed notification and template contract for one incident.
type AlertSnapshot struct {
	// ID identifies one alert lifecycle, including after a resource reopens.
	ID string `json:"id"`
	// Kind is the persisted alert kind.
	Kind string `json:"kind"`
	// Title is the short human-readable description.
	Title string `json:"title"`
	// Message is the safe incident summary.
	Message string `json:"message"`
	// ResourceType identifies the affected resource class.
	ResourceType string `json:"resourceType"`
	// ResourceID identifies the affected resource.
	ResourceID string `json:"resourceId"`
	// OpenedAt marks the beginning of this incident.
	OpenedAt time.Time `json:"openedAt"`
	// ResolvedAt is set for resolution notifications.
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	// Resolution is a human-readable recovery summary.
	Resolution string `json:"resolution,omitempty"`
	// ResolutionReason is a stable recovery code.
	ResolutionReason string `json:"resolutionReason,omitempty"`
}

// AlertRecipient is a physical destination selected when an alert opens.
type AlertRecipient struct {
	// BotIdentity identifies the configured Telegram bot without storing its token.
	BotIdentity string `json:"botIdentity"`
	// ChatID is the actual Telegram chat, independent of its configuration alias.
	ChatID string `json:"chatId"`
	// ThreadID is the actual Telegram topic.
	ThreadID int64 `json:"threadId"`
}

// AlertOpened is an internal durable transition, never a history event.
type AlertOpened struct {
	// Alert is the safe incident snapshot.
	Alert AlertSnapshot `json:"alert"`
	// Recipients captures the physical opening routes at publication time.
	Recipients []AlertRecipient `json:"recipients"`
}

func (*AlertOpened) Type() Type                 { return TypeAlertOpened }
func (*AlertOpened) Message() string            { return "Alert opened" }
func (*AlertOpened) Details() map[string]string { return nil }

// AlertResolved is an internal durable transition, never a history event.
type AlertResolved struct {
	// Alert is the safe resolved incident snapshot.
	Alert AlertSnapshot `json:"alert"`
	// Recipients captures current routing for the case with no opening message.
	Recipients []AlertRecipient `json:"recipients"`
}

func (*AlertResolved) Type() Type                 { return TypeAlertResolved }
func (*AlertResolved) Message() string            { return "Alert resolved" }
func (*AlertResolved) Details() map[string]string { return nil }
