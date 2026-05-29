package compose

import (
	"fmt"

	"github.com/artarts36/specw"
)

type InitJob struct {
	Name        string           `yaml:"name" json:"name"`
	Image       string           `yaml:"image" json:"image"`
	Command     []string         `yaml:"command" json:"command"`
	Environment Environment      `yaml:"environment" json:"environment,omitempty"`
	Networks    *ServiceNetworks `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef      `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef      `yaml:"configs" json:"configs,omitempty"`
	Timeout     specw.Duration   `yaml:"timeout" json:"timeout,omitempty"`
}

func normalizeInitJobs(jobs []InitJob, networks map[string]Network) ([]InitJob, error) {
	for i := range jobs {
		if jobs[i].Image == "" {
			return nil, fmt.Errorf("init-jobs[%d]: image is required", i)
		}

		resolveNetworkAliases(jobs[i].Networks, networks)
		if jobs[i].Name == "" {
			jobs[i].Name = fmt.Sprintf("job-%d", i)
		}
	}

	return jobs, nil
}
