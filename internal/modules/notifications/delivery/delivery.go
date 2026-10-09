// Package delivery remembers sent messages by an opaque correlation key so they can be edited or replied to later.
// It knows nothing about alerts or events: callers provide the key.
package delivery

import "time"

// DefaultTTL is how long a correlation is kept when no explicit TTL is configured.
const DefaultTTL = 24 * time.Hour

// Receipt is a transport-independent result of a delivered message.
type Receipt struct {
	// MessageID identifies the sent message in the transport; empty when the transport does not provide it.
	MessageID string `json:"messageId"`
}
