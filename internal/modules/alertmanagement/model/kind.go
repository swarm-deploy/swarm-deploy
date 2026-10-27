package model

type (
	AlertKind    string
	ResourceType string
)

const (
	AlertKindSyncFailed               AlertKind = "sync_failed"
	AlertKindNodeDisconnected         AlertKind = "node_disconnected"
	AlertKindGitRepositoryUnavailable AlertKind = "git_repository_unavailable"
	AlertKindServiceDrift             AlertKind = "service_drift"

	ResourceTypeStack      ResourceType = "stack"
	ResourceTypeNode       ResourceType = "node"
	ResourceTypeRepository ResourceType = "repository"
	ResourceTypeService    ResourceType = "service"
)

// SyncFailedFingerprint returns the correlation key for a stack synchronization failure.
func SyncFailedFingerprint(stackID string) string {
	return string(AlertKindSyncFailed) + ":" + string(ResourceTypeStack) + ":" + stackID
}

// GitRepositoryUnavailableFingerprint returns the correlation key for repository access failures.
func GitRepositoryUnavailableFingerprint(repository string) string {
	return string(AlertKindGitRepositoryUnavailable) + ":" + string(ResourceTypeRepository) + ":" + repository
}

// ServiceDriftFingerprint returns the correlation key for one drifted service.
func ServiceDriftFingerprint(stackName, serviceName string) string {
	return string(AlertKindServiceDrift) + ":" + string(ResourceTypeService) + ":" + stackName + "/" + serviceName
}

func NodeDisconnectedFingerprint(nodeID string) string {
	return string(AlertKindNodeDisconnected) + ":" + string(ResourceTypeNode) + ":" + nodeID
}
