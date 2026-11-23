package history

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// Entry is a persisted event view returned by API.
type Entry struct {
	// ID uniquely identifies this event.
	ID string `json:"id"`
	// Type is a unique event type.
	Type events.Type `json:"type"`
	// Severity is an event priority level.
	Severity events.Severity `json:"severity"`
	// Category is an event functional group.
	Category events.Category `json:"category"`
	// CreatedAt is event creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// Message is a short human-readable event description.
	Message string `json:"message"`
	// Details contains optional event-specific details like stack, commit or error.
	Details map[string]string `json:"details,omitempty"`
}

func toEntry(now time.Time, envelope events.Envelope) Entry {
	eventType := envelope.Event.Type()
	return Entry{
		ID: envelope.ID, Type: eventType, Severity: eventType.Severity(), Category: eventType.Category(),
		CreatedAt: now, Message: envelope.Event.Message(), Details: cloneDetails(envelope.Event.Details()),
	}
}

func cloneDetails(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
