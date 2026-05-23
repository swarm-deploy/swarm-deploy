package compose

type ServiceDeploy struct {
	EndpointMode   string                       `yaml:"endpoint_mode,omitempty" json:"endpoint_mode,omitempty"`
	Labels         map[string]string            `yaml:"labels,omitempty" json:"labels,omitempty"`
	Mode           string                       `yaml:"mode,omitempty" json:"mode,omitempty"`
	Placement      *ServiceDeployPlacement      `yaml:"placement,omitempty" json:"placement,omitempty"`
	Replicas       *uint64                      `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	Resources      *ServiceDeployResources      `yaml:"resources,omitempty" json:"resources,omitempty"`
	RestartPolicy  *ServiceDeployRestartPolicy  `yaml:"restart_policy,omitempty" json:"restart_policy,omitempty"`
	RollbackConfig *ServiceDeployRollbackConfig `yaml:"rollback_config,omitempty" json:"rollback_config,omitempty"`
	UpdateConfig   *ServiceDeployUpdateConfig   `yaml:"update_config,omitempty" json:"update_config,omitempty"`
}

type ServiceDeployUpdateConfig struct {
	Parallelism     *uint64  `yaml:"parallelism,omitempty" json:"parallelism,omitempty"`
	Delay           string   `yaml:"delay,omitempty" json:"delay,omitempty"`
	FailureAction   string   `yaml:"failure_action,omitempty" json:"failure_action,omitempty"`
	Monitor         string   `yaml:"monitor,omitempty" json:"monitor,omitempty"`
	MaxFailureRatio *float64 `yaml:"max_failure_ratio,omitempty" json:"max_failure_ratio,omitempty"`
	Order           string   `yaml:"order,omitempty" json:"order,omitempty"`
}

type ServiceDeployRollbackConfig struct {
	Parallelism     *uint64  `yaml:"parallelism,omitempty" json:"parallelism,omitempty"`
	Delay           string   `yaml:"delay,omitempty" json:"delay,omitempty"`
	FailureAction   string   `yaml:"failure_action,omitempty" json:"failure_action,omitempty"`
	Monitor         string   `yaml:"monitor,omitempty" json:"monitor,omitempty"`
	MaxFailureRatio *float64 `yaml:"max_failure_ratio,omitempty" json:"max_failure_ratio,omitempty"`
	Order           string   `yaml:"order,omitempty" json:"order,omitempty"`
}

type ServiceDeployRestartPolicy struct {
	Condition   string  `yaml:"condition,omitempty" json:"condition,omitempty"`
	Delay       string  `yaml:"delay,omitempty" json:"delay,omitempty"`
	MaxAttempts *uint64 `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty"`
	Window      string  `yaml:"window,omitempty" json:"window,omitempty"`
}

type ServiceDeployResources struct {
	Limits       *ServiceDeployResource `yaml:"limits,omitempty" json:"limits,omitempty"`
	Reservations *ServiceDeployResource `yaml:"reservations,omitempty" json:"reservations,omitempty"`
}

type ServiceDeployResource struct {
	Cpus   string  `yaml:"cpus,omitempty" json:"cpus,omitempty"`
	Memory string  `yaml:"memory,omitempty" json:"memory,omitempty"`
	Pids   *uint64 `yaml:"pids,omitempty" json:"pids,omitempty"`
}

type ServiceDeployPlacement struct {
	Constraints        []string                           `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	Preferences        []ServiceDeployPlacementPreference `yaml:"preferences,omitempty" json:"preferences,omitempty"`
	MaxReplicasPerNode *uint64                            `yaml:"max_replicas_per_node,omitempty" json:"max_replicas_per_node,omitempty"`
}

type ServiceDeployPlacementPreference struct {
	Spread string `yaml:"spread,omitempty" json:"spread,omitempty"`
}
