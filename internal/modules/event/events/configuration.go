package events

// ConfigurationChanged is emitted after a distinct configuration has been
// accepted. Repository fields are set only when GitOps detects a configured
// repository transition before desired-state reload can complete.
type ConfigurationChanged struct {
	PreviousRepository string
	Repository         string
}

// Type returns the internal configuration lifecycle event type.
func (*ConfigurationChanged) Type() Type { return TypeConfigurationChanged }

// Message returns a diagnostic description; this event is not user-visible.
func (*ConfigurationChanged) Message() string { return "Configuration changed" }

// Details returns no domain payload for this system notification.
func (*ConfigurationChanged) Details() map[string]string { return nil }
