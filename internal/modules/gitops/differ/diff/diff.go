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

	// HasChanges reports whether the service contains any supported semantic changes.
	HasChanges bool `json:"hasChanges"`

	// Image contains image change details. Nil when image is unchanged.
	Image *ImageDiff `json:"image,omitempty"`
	// Environment contains changed service environment variables.
	Environment []EnvironmentDiff `json:"environment,omitempty"`
	// Networks contains changed service network attachments.
	Networks []NetworkDiff `json:"networks,omitempty"`
	// Secrets contains changed service secrets.
	Secrets []SecretDiff `json:"secrets,omitempty"`
	// Volumes contains changed service volume mounts.
	Volumes []VolumeDiff `json:"volumes,omitempty"`
	// Configs contains changed service config mounts.
	Configs []ConfigDiff `json:"configs,omitempty"`
	// Ports contains changed published service ports.
	Ports []PortDiff `json:"ports,omitempty"`
	// InitJobs contains changed init jobs represented as nested service differences.
	InitJobs []ServiceDiff `json:"initJobs,omitempty"`
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

// VolumeDiff describes one changed volume mount.
type VolumeDiff struct {
	// Type is the volume mount type.
	Type string `json:"type,omitempty"`
	// Source is the volume name or host path.
	Source string `json:"source,omitempty"`
	// Target is the mount path in the service container.
	Target string `json:"target"`
	// ReadOnly reports whether the mount is read-only.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Consistency is the mount consistency requirement.
	Consistency string `json:"consistency,omitempty"`
	// Added reports that the volume mount was added.
	Added bool `json:"added,omitempty"`
	// Removed reports that the volume mount was removed.
	Removed bool `json:"removed,omitempty"`
}

// ConfigDiff describes one changed config mount.
type ConfigDiff struct {
	// Name is the config name.
	Name string `json:"name"`
	// MountFile is the target mount path in the service container.
	MountFile string `json:"mountFile,omitempty"`
	// UID is the numeric user ID that owns the mounted config.
	UID string `json:"uid,omitempty"`
	// GID is the numeric group ID that owns the mounted config.
	GID string `json:"gid,omitempty"`
	// Mode is the file mode of the mounted config.
	Mode *uint32 `json:"mode,omitempty"`
	// Added reports that the config mount was added.
	Added bool `json:"added,omitempty"`
	// Removed reports that the config mount was removed.
	Removed bool `json:"removed,omitempty"`
}

// PortDiff describes one changed published service port.
type PortDiff struct {
	// Published is the port exposed by the swarm service.
	Published int `json:"published"`
	// Target is the port exposed by the container.
	Target int `json:"target"`
	// Protocol is the transport protocol.
	Protocol string `json:"protocol"`
	// AppProtocol is the application protocol hint.
	AppProtocol string `json:"appProtocol,omitempty"`
	// Mode is the swarm publish mode.
	Mode string `json:"mode"`
	// HostIP is the host address to bind.
	HostIP string `json:"hostIP,omitempty"`
	// Added reports that the port mapping was added.
	Added bool `json:"added,omitempty"`
	// Removed reports that the port mapping was removed.
	Removed bool `json:"removed,omitempty"`
}

func (d *ServiceDiff) CalcHasChanges() {
	d.HasChanges = d.Image != nil ||
		len(d.Environment) > 0 ||
		len(d.Networks) > 0 ||
		len(d.Secrets) > 0 ||
		len(d.Volumes) > 0 ||
		len(d.Configs) > 0 ||
		len(d.Ports) > 0 ||
		len(d.InitJobs) > 0
}
