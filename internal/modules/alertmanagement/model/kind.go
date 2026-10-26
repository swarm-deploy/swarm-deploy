package model

type (
	AlertKind    string
	ResourceType string
)

const (
	AlertKindSyncFailed               AlertKind = "sync_failed"
	AlertKindNodeDisconnected         AlertKind = "node_disconnected"
	AlertKindGitRepositoryUnavailable AlertKind = "git_repository_unavailable"

	ResourceTypeStack      ResourceType = "stack"
	ResourceTypeNode       ResourceType = "node"
	ResourceTypeRepository ResourceType = "repository"
)

// SyncFailedFingerprint returns the correlation key for a stack synchronization failure.
func SyncFailedFingerprint(stackID string) string {
	return string(AlertKindSyncFailed) + ":" + string(ResourceTypeStack) + ":" + stackID
}

// GitRepositoryUnavailableFingerprint returns the correlation key for repository access failures.
func GitRepositoryUnavailableFingerprint(repository string) string {
	return string(AlertKindGitRepositoryUnavailable) + ":" + string(ResourceTypeRepository) + ":" + repository
}

func NodeDisconnectedFingerprint(nodeID string) string {
	return string(AlertKindNodeDisconnected) + string(ResourceTypeNode) + ":" + nodeID
}
