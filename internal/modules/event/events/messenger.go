package events

// MessengerUnavailable reports a failed messenger connectivity attempt.
type MessengerUnavailable struct {
	// ResourceID is the notification-owned logical messenger identity.
	ResourceID string
	// ErrorCode is a stable, credential-free failure category.
	ErrorCode string
	// Description is a safe human-readable diagnostic.
	Description string
}

func (*MessengerUnavailable) Type() Type                 { return TypeMessengerUnavailable }
func (e *MessengerUnavailable) Message() string          { return e.Description }
func (*MessengerUnavailable) Details() map[string]string { return nil }

// MessengerAvailable reports that a messenger API is reachable.
type MessengerAvailable struct {
	// ResourceID is the notification-owned logical messenger identity.
	ResourceID string
}

func (*MessengerAvailable) Type() Type                 { return TypeMessengerAvailable }
func (*MessengerAvailable) Message() string            { return "Messenger is available" }
func (*MessengerAvailable) Details() map[string]string { return nil }

// MessengerMisconfigured reports a rejected logical channel configuration.
type MessengerMisconfigured struct {
	// ResourceID is the notification-owned logical channel identity.
	ResourceID string
	// ErrorCode is a stable, credential-free failure category.
	ErrorCode string
	// Description is a safe human-readable diagnostic.
	Description string
}

func (*MessengerMisconfigured) Type() Type                 { return TypeMessengerMisconfigured }
func (e *MessengerMisconfigured) Message() string          { return e.Description }
func (*MessengerMisconfigured) Details() map[string]string { return nil }

// MessengerDeliverySucceeded reports an accepted notification operation.
type MessengerDeliverySucceeded struct {
	// ResourceID is the notification-owned logical channel identity.
	ResourceID string
}

func (*MessengerDeliverySucceeded) Type() Type                 { return TypeMessengerDeliverySucceeded }
func (*MessengerDeliverySucceeded) Message() string            { return "Messenger delivery succeeded" }
func (*MessengerDeliverySucceeded) Details() map[string]string { return nil }
