package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Services map[string]Service

type Service struct {
	Name        string      `yaml:"name" json:"name"`
	Image       string      `yaml:"image" json:"image"`
	Environment Environment `yaml:"environment" json:"environment,omitempty"`
	Networks    []string    `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef `yaml:"configs" json:"configs,omitempty"`
	InitJobs    []InitJob   `yaml:"x-init-deploy-jobs" json:"init_jobs,omitempty"`
}

func (s *Services) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

	name := ""

	for i, cn := range n.Content {
		if i%2 == 0 {
			name = cn.Value
			continue
		}

		var srv Service

		err := cn.Decode(srv)
		if err != nil {
			return fmt.Errorf("decode service with name %q: %w", name, err)
		}

		srv.Name = name

		(*s)[name] = srv
	}

	return nil
}
