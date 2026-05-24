package compose

import (
	"fmt"

	"github.com/artarts36/specw"
)

type InitJob struct {
	Name        string            `yaml:"name" json:"name"`
	Image       specw.String      `yaml:"image" json:"image"`
	Command     []string          `yaml:"command" json:"command"`
	Environment Environment       `yaml:"environment" json:"environment,omitempty"`
	Networks    []*ServiceNetwork `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef       `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef       `yaml:"configs" json:"configs,omitempty"`
	Timeout     specw.Duration    `yaml:"timeout" json:"timeout,omitempty"`
}

func normalizeInitJobs(jobs []InitJob, networks map[string]Network) []InitJob {
	for i := range jobs {
		if jobs[i].Name == "" {
			jobs[i].Name = fmt.Sprintf("job-%d", i)
		}
		resolveNetworkAliases(jobs[i].Networks, networks)
	}

	return jobs
}

func (job *InitJob) NetworkNames() []string {
	names := make([]string, len(job.Networks))

	for i, network := range job.Networks {
		names[i] = network.Name
	}

	return names
}
