package events

// SyncManualStarted is emitted when a manual sync run starts.
type SyncManualStarted struct{}

func (m *SyncManualStarted) Type() Type {
	return TypeSyncManualStarted
}
