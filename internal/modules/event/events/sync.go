package events

// SyncManualStarted is emitted when a manual sync run starts.
type SyncManualStarted struct {
	// Username identifies the user who started the manual sync.
	Username string
}

func (m *SyncManualStarted) Type() Type {
	return TypeSyncManualStarted
}

func (m *SyncManualStarted) Message() string {
	return "Manual sync started"
}

func (m *SyncManualStarted) Details() map[string]string {
	details := map[string]string{}

	if m.Username != "" {
		details[AttributeUsername] = m.Username
	}

	return details
}

func (m *SyncManualStarted) WithUsername(username string) Event {
	return &SyncManualStarted{
		Username: username,
	}
}
