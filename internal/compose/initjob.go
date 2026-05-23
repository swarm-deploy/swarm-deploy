package compose

import (
	"fmt"

	"github.com/artarts36/specw"
)

type InitJob struct {
	Name        string         `yaml:"name" json:"name"`
	Image       string         `yaml:"image" json:"image"`
	Command     []string       `yaml:"command" json:"command"`
	Environment Environment    `yaml:"environment" json:"environment,omitempty"`
	Networks    []string       `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef    `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef    `yaml:"configs" json:"configs,omitempty"`
	Timeout     specw.Duration `yaml:"timeout" json:"timeout,omitempty"`
}

func normalizeInitJobs(jobs []InitJob, networks map[string]Network) ([]InitJob, error) {
	for i := range jobs {
		if jobs[i].Name == "" {
			jobs[i].Name = fmt.Sprintf("job-%d", i)
		}
		if jobs[i].Image == "" {
			return nil, fmt.Errorf("item %d image is required", i)
		}
		jobs[i].Networks = resolveNetworkAliases(jobs[i].Networks, networks)
		jobs[i].Secrets = normalizeObjectRefs(jobs[i].Secrets)
		jobs[i].Configs = normalizeObjectRefs(jobs[i].Configs)
	}

	return jobs, nil
}
