package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type ObjectRef struct {
	Source string `yaml:"source" json:"source"`
	Target string `yaml:"target" json:"target,omitempty"`
}

func (ref *ObjectRef) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*ref = ObjectRef{
			Source: n.Value,
			Target: "/run/secrets/" + n.Value,
		}
		return nil
	}

	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

	var schema struct {
		Source string `yaml:"source" json:"source"`
		Target string `yaml:"target" json:"target,omitempty"`
	}

	err := n.Decode(&schema)
	if err != nil {
		return err
	}

	*ref = schema

	return nil
}
