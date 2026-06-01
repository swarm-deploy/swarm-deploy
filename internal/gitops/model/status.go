package model

type SyncStatus string

const (
	SyncStatusSynced    = "Synced"
	SyncStatusOutOfSync = "OutOfSync"
)

// StackStatus contains aggregated deployment counters for a stack.
type StackStatus struct {
	// Synced is the number of services currently marked as synced.
	Synced int `json:"synced"`
	// OutOfSynced is the number of services currently marked as out of sync.
	OutOfSynced int `json:"out_of_synced"`
}

// NewStackStatus builds aggregated stack status counters from per-service state.
func NewStackStatus(services map[string]Service) StackStatus {
	status := StackStatus{}

	for _, service := range services {
		switch service.SyncStatus {
		case SyncStatusSynced:
			status.Synced++
		case SyncStatusOutOfSync:
			status.OutOfSynced++
		}
	}

	return status
}
