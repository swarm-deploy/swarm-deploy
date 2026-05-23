package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// SharedObject wraps configs and secrets in compose top level.
type SharedObject struct {
	Alias string `yaml:"-"`

	Name string `yaml:"name" json:"name"`

	File     string `yaml:"file,omitempty" json:"file,omitempty"`
	Driver   string `yaml:"drive,omitempty" json:"driver"`
	External bool   `yaml:"external,omitempty" json:"external"`
}

type SharedObjects map[string]*SharedObject

func (s *SharedObjects) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

	*s = map[string]*SharedObject{}

	alias := ""

	for i, cn := range n.Content {
		if i%2 == 0 {
			alias = cn.Value
			continue
		}

		var cos SharedObject

		err := cn.Decode(&cos)
		if err != nil {
			return fmt.Errorf("decode config/secret with key %q: %w", alias, err)
		}

		cos.Alias = alias

		(*s)[alias] = &cos
	}

	return nil
}
