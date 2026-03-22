package events

// SyncManualStarted is emitted when a manual sync run starts.
type SyncManualStarted struct{}

func (m *SyncManualStarted) Type() Type {
	return TypeSyncManualStarted
}

func (m *SyncManualStarted) Message() string {
	return "Manual sync started"
}

func (m *SyncManualStarted) Details() map[string]string {
	return nil
}
