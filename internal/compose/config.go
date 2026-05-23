package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// SharedObject wraps configs and secrets in compose top level.
type SharedObject struct {
	Name string `yaml:"name" json:"name"` // alias from map.

	File     string `yaml:"file" json:"file,omitempty"`
	Driver   string `yaml:"driver" json:"driver"`
	External bool   `yaml:"external" json:"external"`
}

type SharedObjects map[string]SharedObject

func (s *SharedObjects) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

	name := ""

	for i, cn := range n.Content {
		if i%2 == 0 {
			name = cn.Value
			continue
		}

		var cos SharedObject

		err := cn.Decode(cos)
		if err != nil {
			return fmt.Errorf("decode config/secret with key %q: %w", name, err)
		}

		cos.Name = name

		(*s)[name] = cos
	}

	return nil
}
