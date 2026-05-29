package events

// SyncManualStarted is emitted when a manual sync run starts.
type SyncManualStarted struct {
	TriggeredBy string
}

func (m *SyncManualStarted) Type() Type {
	return TypeSyncManualStarted
}

func (m *SyncManualStarted) Message() string {
	return "Manual sync started"
}

func (m *SyncManualStarted) Details() map[string]string {
	details := map[string]string{}

	if m.TriggeredBy != "" {
		details["triggered_by"] = m.TriggeredBy
	}

	return details
}

func (m *SyncManualStarted) WithUsername(username string) Event {
	return &SyncManualStarted{
		TriggeredBy: username,
	}
}
