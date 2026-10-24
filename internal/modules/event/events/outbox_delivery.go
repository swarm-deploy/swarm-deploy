package events

// OutboxDelivery identifies a safely monitored notification delivery.
type OutboxDelivery struct {
	// EventID identifies the source Outbox publication.
	EventID string
	// SubscriptionID identifies the configured delivery route.
	SubscriptionID string
	// DestinationKey identifies the physical destination without exposing its address or credentials.
	DestinationKey string
	// EventType identifies the source event being delivered.
	EventType TypeName
	// Destination identifies the notification transport kind.
	Destination string
	// Channel identifies the configured non-secret channel alias.
	Channel string
}

// OutboxDeliveryFailed is emitted once for each monitored terminal transition.
type OutboxDeliveryFailed struct {
	// OutboxDelivery contains the safe source and destination metadata.
	OutboxDelivery
	// ErrorCode is the stable sanitized delivery failure classification.
	ErrorCode string
}

func (*OutboxDeliveryFailed) Type() Type { return TypeOutboxDeliveryFailed }
func (*OutboxDeliveryFailed) Message() string {
	return "Notification delivery permanently failed"
}
func (*OutboxDeliveryFailed) Details() map[string]string { return nil }

// OutboxDeliveryRecovered is emitted after all replayed terminal deliveries for a route succeed.
type OutboxDeliveryRecovered struct {
	// OutboxDelivery contains the safe source and destination metadata.
	OutboxDelivery
}

func (*OutboxDeliveryRecovered) Type() Type { return TypeOutboxDeliveryRecovered }
func (*OutboxDeliveryRecovered) Message() string {
	return "Notification delivery recovered"
}
func (*OutboxDeliveryRecovered) Details() map[string]string { return nil }

// OutboxDeliveryDiscarded is emitted when an operator waives the final terminal delivery for a route.
type OutboxDeliveryDiscarded struct {
	// OutboxDelivery contains the safe source and destination metadata.
	OutboxDelivery
}

func (*OutboxDeliveryDiscarded) Type() Type { return TypeOutboxDeliveryDiscarded }
func (*OutboxDeliveryDiscarded) Message() string {
	return "Notification delivery failure discarded"
}
func (*OutboxDeliveryDiscarded) Details() map[string]string { return nil }
