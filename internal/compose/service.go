package compose

type Service struct {
	Name        string      `yaml:"name" json:"name"`
	Image       string      `yaml:"image" json:"image"`
	Environment Environment `yaml:"environment" json:"environment,omitempty"`
	Networks    []string    `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef `yaml:"configs" json:"configs,omitempty"`
	InitJobs    []InitJob   `yaml:"x-init-deploy-jobs" json:"init_jobs,omitempty"`
}
