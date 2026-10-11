package compose

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/artarts36/specw"
)

type InitJob struct {
	Name        string           `yaml:"name" json:"name"`
	Image       string           `yaml:"image" json:"image"`
	Entrypoint  []string         `yaml:"entrypoint" json:"entrypoint,omitempty"`
	Command     []string         `yaml:"command" json:"command"`
	Environment Environment      `yaml:"environment" json:"environment,omitempty"`
	Networks    *ServiceNetworks `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef      `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef      `yaml:"configs" json:"configs,omitempty"`
	Timeout     specw.Duration   `yaml:"timeout" json:"timeout,omitempty"`
}

type initJobJSON struct {
	// Name identifies the init job.
	Name string `json:"name"`
	// Image is the init container image.
	Image string `json:"image"`
	// Entrypoint overrides the image entrypoint.
	Entrypoint []string `json:"entrypoint,omitempty"`
	// Command contains init container arguments.
	Command []string `json:"command"`
	// Environment contains init container environment values.
	Environment Environment `json:"environment,omitempty"`
	// Networks contains init container network attachments.
	Networks *ServiceNetworks `json:"networks,omitempty"`
	// Secrets contains init container secret references.
	Secrets []ObjectRef `json:"secrets,omitempty"`
	// Configs contains init container config references.
	Configs []ObjectRef `json:"configs,omitempty"`
	// Timeout is the effective timeout in nanoseconds.
	Timeout int64 `json:"timeout,omitempty"`
}

// MarshalJSON stores the effective timeout as nanoseconds instead of exposing
// specw.Duration's implementation struct in desired-state snapshots.
func (j InitJob) MarshalJSON() ([]byte, error) {
	return json.Marshal(initJobJSON{
		Name: j.Name, Image: j.Image, Entrypoint: j.Entrypoint, Command: j.Command,
		Environment: j.Environment, Networks: j.Networks, Secrets: j.Secrets, Configs: j.Configs,
		Timeout: int64(j.Timeout.Value),
	})
}

// UnmarshalJSON restores an init job persisted in a desired-state snapshot.
func (j *InitJob) UnmarshalJSON(data []byte) error {
	var value initJobJSON
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	j.Name, j.Image = value.Name, value.Image
	j.Entrypoint, j.Command = value.Entrypoint, value.Command
	j.Environment, j.Networks = value.Environment, value.Networks
	j.Secrets, j.Configs = value.Secrets, value.Configs
	j.Timeout.Value = time.Duration(value.Timeout)
	return nil
}

func normalizeInitJobs(jobs []InitJob, networks map[string]Network) ([]InitJob, []ValidationIssue) {
	var issues []ValidationIssue
	for i := range jobs {
		if jobs[i].Image == "" {
			issues = append(issues, ValidationIssue{
				Field:   fmt.Sprintf("init-jobs[%d].image", i),
				Code:    IssueCodeRequired,
				Message: "image is required",
			})
			continue
		}

		resolveNetworkAliases(jobs[i].Networks, networks)
		if jobs[i].Name == "" {
			jobs[i].Name = fmt.Sprintf("job-%d", i)
		}
	}

	return jobs, issues
}
