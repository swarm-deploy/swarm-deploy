package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Services []Service

type Service struct {
	Name        string           `yaml:"-" json:"name"`
	Image       string           `yaml:"image" json:"image"`
	Command     Command          `yaml:"command" json:"command"`
	Healthcheck *ServiceHealth   `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
	Ports       ServicePorts     `yaml:"ports,omitempty" json:"ports,omitempty"`
	Networks    *ServiceNetworks `yaml:"networks,omitempty" json:"networks,omitempty"`
	Secrets     []ObjectRef      `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Configs     []ObjectRef      `yaml:"configs,omitempty" json:"configs,omitempty"`
	Labels      Labels           `yaml:"labels,omitempty" json:"labels,omitempty"`
	EnvFiles    []string         `yaml:"env_file,omitempty" json:"env_file,omitempty"`
	Environment Environment      `yaml:"environment,omitempty" json:"environment,omitempty"`
	InitJobs    []InitJob        `yaml:"x-init-deploy-jobs,omitempty" json:"init_jobs,omitempty"`
	Deploy      ServiceDeploy    `yaml:"deploy,omitempty" json:"deploy"`
	Logging     ServiceLogging   `yaml:"logging,omitempty" json:"logging,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`
}

type ServiceHealth struct {
	Test          Command `yaml:"test,omitempty" json:"test,omitempty"`
	Interval      string  `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout       string  `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retries       *uint64 `yaml:"retries,omitempty" json:"retries,omitempty"`
	StartPeriod   string  `yaml:"start_period,omitempty" json:"start_period,omitempty"`
	StartInterval string  `yaml:"start_interval,omitempty" json:"start_interval,omitempty"`
	Disable       bool    `yaml:"disable,omitempty" json:"disable,omitempty"`
}

type ServiceLogging struct {
	Driver  string            `yaml:"driver,omitempty" json:"driver,omitempty"`
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`
}

func (s *Services) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

	name := ""
	services := make([]Service, 0)

	for i, cn := range n.Content {
		if i%2 == 0 {
			name = cn.Value
			continue
		}

		var srv Service

		err := cn.Decode(&srv)
		if err != nil {
			return fmt.Errorf("decode service with name %q: %w", name, err)
		}

		srv.Name = name

		services = append(services, srv)
	}

	*s = services

	return nil
}

func (s Services) MarshalYAML() (interface{}, error) {
	const nodesMul = 2

	root := yaml.Node{
		Kind:    yaml.MappingNode,
		Content: make([]*yaml.Node, 0, nodesMul*len(s)),
	}

	for _, service := range s {
		root.Content = append(root.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: service.Name,
		})

		node := &yaml.Node{
			Kind: yaml.MappingNode,
		}

		err := node.Encode(service)
		if err != nil {
			return nil, fmt.Errorf("encode service with name %q: %w", service.Name, err)
		}

		root.Content = append(root.Content, node)
	}

	return &root, nil
}
