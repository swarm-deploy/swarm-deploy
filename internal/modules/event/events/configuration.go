package events

// ConfigurationChanged is emitted after a distinct configuration has been
// successfully accepted. Consumers read the current accepted state directly.
type ConfigurationChanged struct{}

// Type returns the internal configuration lifecycle event type.
func (*ConfigurationChanged) Type() Type { return TypeConfigurationChanged }

// Message returns a diagnostic description; this event is not user-visible.
func (*ConfigurationChanged) Message() string { return "Configuration changed" }

// Details returns no domain payload for this system notification.
func (*ConfigurationChanged) Details() map[string]string { return nil }
