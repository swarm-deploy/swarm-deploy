package diff

// ScalarDiff describes an old/new scalar transition.
type ScalarDiff[T comparable] struct {
	// Old is the value before the change.
	Old T `json:"old"`
	// New is the value after the change.
	New T `json:"new"`
}

// CommandDiff describes an ordered command transition.
type CommandDiff struct {
	// Old is the command before the change.
	Old []string `json:"old"`
	// New is the command after the change.
	New []string `json:"new"`
}

// StringListDiff describes a transition of an ordered string list.
type StringListDiff struct {
	// Old is the ordered list before the change.
	Old []string `json:"old"`
	// New is the ordered list after the change.
	New []string `json:"new"`
}

// StringSetDiff describes changes to an unordered collection of strings.
type StringSetDiff struct {
	// Added contains values only present after the change.
	Added []string `json:"added,omitempty"`
	// Removed contains values only present before the change.
	Removed []string `json:"removed,omitempty"`
}

// LabelDiff describes one changed label.
type LabelDiff struct {
	// Key is the label key.
	Key string `json:"key"`
	// Old is the value before the change.
	Old string `json:"old,omitempty"`
	// New is the value after the change.
	New string `json:"new,omitempty"`
	// Added reports that the label was added.
	Added bool `json:"added,omitempty"`
	// Changed reports that the label value changed.
	Changed bool `json:"changed,omitempty"`
	// Removed reports that the label was removed.
	Removed bool `json:"removed,omitempty"`
}

// LoggingOptionDiff describes one changed logging option.
type LoggingOptionDiff struct {
	// Key is the option key.
	Key string `json:"key"`
	// Old is the value before the change.
	Old string `json:"old,omitempty"`
	// New is the value after the change.
	New string `json:"new,omitempty"`
	// Added reports that the option was added.
	Added bool `json:"added,omitempty"`
	// Changed reports that the option value changed.
	Changed bool `json:"changed,omitempty"`
	// Removed reports that the option was removed.
	Removed bool `json:"removed,omitempty"`
}

// LoggingDiff describes field-level logging changes.
type LoggingDiff struct {
	// Driver contains a logging driver transition.
	Driver *ScalarDiff[string] `json:"driver,omitempty"`
	// Options contains per-key logging option changes sorted by key.
	Options []LoggingOptionDiff `json:"options,omitempty"`
}

// HealthcheckDiff describes field-level healthcheck changes.
type HealthcheckDiff struct {
	// Test contains an ordered healthcheck command transition.
	Test *CommandDiff `json:"test,omitempty"`
	// Interval contains an interval transition.
	Interval *ScalarDiff[string] `json:"interval,omitempty"`
	// Timeout contains a timeout transition.
	Timeout *ScalarDiff[string] `json:"timeout,omitempty"`
	// Retries contains a retries transition.
	Retries *ScalarDiff[uint64] `json:"retries,omitempty"`
	// StartPeriod contains a start period transition.
	StartPeriod *ScalarDiff[string] `json:"startPeriod,omitempty"`
	// StartInterval contains a start interval transition.
	StartInterval *ScalarDiff[string] `json:"startInterval,omitempty"`
	// Disable contains a disabled-state transition.
	Disable *ScalarDiff[bool] `json:"disable,omitempty"`
}

// DeployDiff describes field-level deploy changes.
type DeployDiff struct {
	// EndpointMode contains an endpoint mode transition.
	EndpointMode *ScalarDiff[string] `json:"endpointMode,omitempty"`
	// Labels contains per-key deploy label changes.
	Labels []LabelDiff `json:"labels,omitempty"`
	// Mode contains a deploy mode transition.
	Mode *ScalarDiff[string] `json:"mode,omitempty"`
	// Replicas contains a replicas transition.
	Replicas *ScalarDiff[uint64] `json:"replicas,omitempty"`
	// Placement contains placement changes.
	Placement *PlacementDiff `json:"placement,omitempty"`
	// Resources contains resource changes.
	Resources *ResourcesDiff `json:"resources,omitempty"`
	// RestartPolicy contains restart policy changes.
	RestartPolicy *RestartPolicyDiff `json:"restartPolicy,omitempty"`
	// RollbackConfig contains rollback configuration changes.
	RollbackConfig *RollbackConfigDiff `json:"rollbackConfig,omitempty"`
	// UpdateConfig contains update configuration changes.
	UpdateConfig *UpdateConfigDiff `json:"updateConfig,omitempty"`
}

// PlacementDiff describes field-level placement changes.
type PlacementDiff struct {
	// Constraints contains added and removed placement constraints.
	Constraints *StringSetDiff `json:"constraints,omitempty"`
	// Preferences contains an ordered placement preference transition.
	Preferences *StringListDiff `json:"preferences,omitempty"`
	// MaxReplicasPerNode contains a per-node replica limit transition.
	MaxReplicasPerNode *ScalarDiff[uint64] `json:"maxReplicasPerNode,omitempty"`
}

// ResourcesDiff describes deploy resource changes.
type ResourcesDiff struct {
	// Limits contains resource limit changes.
	Limits *ResourceValuesDiff `json:"limits,omitempty"`
	// Reservations contains resource reservation changes.
	Reservations *ResourceValuesDiff `json:"reservations,omitempty"`
}

// ResourceValuesDiff describes CPU, memory, and process limit changes.
type ResourceValuesDiff struct {
	// CPUs contains a CPU quantity transition.
	CPUs *ScalarDiff[string] `json:"cpus,omitempty"`
	// Memory contains a memory quantity transition.
	Memory *ScalarDiff[string] `json:"memory,omitempty"`
	// PIDs contains a process limit transition.
	PIDs *ScalarDiff[uint64] `json:"pids,omitempty"`
}

// RestartPolicyDiff describes restart policy changes.
type RestartPolicyDiff struct {
	// Condition contains a restart condition transition.
	Condition *ScalarDiff[string] `json:"condition,omitempty"`
	// Delay contains a restart delay transition.
	Delay *ScalarDiff[string] `json:"delay,omitempty"`
	// MaxAttempts contains a maximum attempts transition.
	MaxAttempts *ScalarDiff[uint64] `json:"maxAttempts,omitempty"`
	// Window contains an evaluation window transition.
	Window *ScalarDiff[string] `json:"window,omitempty"`
}

// UpdateConfigDiff describes rolling update configuration changes.
type UpdateConfigDiff struct {
	// Parallelism contains a parallelism transition.
	Parallelism *ScalarDiff[uint64] `json:"parallelism,omitempty"`
	// Delay contains a delay transition.
	Delay *ScalarDiff[string] `json:"delay,omitempty"`
	// FailureAction contains a failure action transition.
	FailureAction *ScalarDiff[string] `json:"failureAction,omitempty"`
	// Monitor contains a monitor duration transition.
	Monitor *ScalarDiff[string] `json:"monitor,omitempty"`
	// MaxFailureRatio contains a maximum failure ratio transition.
	MaxFailureRatio *ScalarDiff[float64] `json:"maxFailureRatio,omitempty"`
	// Order contains an update order transition.
	Order *ScalarDiff[string] `json:"order,omitempty"`
}

// RollbackConfigDiff describes rollback configuration changes.
type RollbackConfigDiff struct {
	// Parallelism contains a parallelism transition.
	Parallelism *ScalarDiff[uint64] `json:"parallelism,omitempty"`
	// Delay contains a delay transition.
	Delay *ScalarDiff[string] `json:"delay,omitempty"`
	// FailureAction contains a failure action transition.
	FailureAction *ScalarDiff[string] `json:"failureAction,omitempty"`
	// Monitor contains a monitor duration transition.
	Monitor *ScalarDiff[string] `json:"monitor,omitempty"`
	// MaxFailureRatio contains a maximum failure ratio transition.
	MaxFailureRatio *ScalarDiff[float64] `json:"maxFailureRatio,omitempty"`
	// Order contains a rollback order transition.
	Order *ScalarDiff[string] `json:"order,omitempty"`
}
