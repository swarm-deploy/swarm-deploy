package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type ServiceNetwork struct {
	Alias string `yaml:"-"`

	// Resolved full name
	Name string `yaml:"-"`
}

func (s *ServiceNetwork) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Alias = node.Value
		return nil
	}

	return fmt.Errorf("expected string node, got %s", node.Tag)
}

func (s *ServiceNetwork) MarshalYAML() (interface{}, error) {
	return s.Alias, nil
}
