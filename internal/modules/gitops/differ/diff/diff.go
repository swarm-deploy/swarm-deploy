package diff

// Diff is a per-service compose changeset.
type Diff struct {
	// Services contains changed services.
	Services []ServiceDiff `json:"services"`
}

// ServiceDiff describes changed entities for one service.
type ServiceDiff struct {
	// ServiceName is a changed service name.
	ServiceName string `json:"serviceName"`
	// StackName is a stack where service belongs.
	StackName string `json:"stackName"`

	HasChanges bool `json:"hasChanges"`

	// Image contains image change details. Nil when image is unchanged.
	Image *ImageDiff `json:"image,omitempty"`
	// Environment contains changed service environment variables.
	Environment []EnvironmentDiff `json:"environment,omitempty"`
	// Networks contains changed service network attachments.
	Networks []NetworkDiff `json:"networks,omitempty"`
	// Secrets contains changed service secrets.
	Secrets []SecretDiff `json:"secrets,omitempty"`
}

// ImageDiff describes image value transition.
type ImageDiff struct {
	// Old is image before change.
	Old string `json:"old"`
	// New is image after change.
	New string `json:"new"`
}

// EnvironmentDiff describes one changed environment variable.
type EnvironmentDiff struct {
	// VarName is an environment variable name.
	VarName string `json:"varName"`
	// Value is a current variable value for add/change and old value for delete.
	Value string `json:"value"`
	// Added reports that variable is newly added.
	Added bool `json:"added,omitempty"`
	// Changed reports that variable value has changed.
	Changed bool `json:"changed,omitempty"`
	// Deleted reports that variable was removed.
	Deleted bool `json:"deleted,omitempty"`
}

// NetworkDiff describes one changed network connection.
type NetworkDiff struct {
	// Name is a network name.
	Name string `json:"name"`
	// Connected reports whether service is connected to this network after commit.
	Connected bool `json:"connected"`
}

// SecretDiff describes one changed secret mount.
type SecretDiff struct {
	// Name is a secret name.
	Name string `json:"name"`
	// MountFile is a target mount path in service container.
	MountFile string `json:"mountFile,omitempty"`
	// Added reports that secret mount was added.
	Added bool `json:"added,omitempty"`
	// Removed reports that secret mount was removed.
	Removed bool `json:"removed,omitempty"`
}
