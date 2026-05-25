package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Volumes map[string]*Volume

type Volume struct {
	Alias string `yaml:"-" json:"-"`

	Name     string            `yaml:"name" json:"name"`
	External string            `yaml:"external,omitempty" json:"external,omitempty"`
	Labels   map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`

	Driver     string            `yaml:"driver,omitempty" json:"driver,omitempty"`
	DriverOpts map[string]string `yaml:"driver_opts,omitempty" json:"driver_opts,omitempty"`
}

func (s *Volumes) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

	*s = map[string]*Volume{}

	alias := ""

	for i, cn := range n.Content {
		if i%2 == 0 {
			alias = cn.Value
			continue
		}

		var vol Volume

		err := cn.Decode(&vol)
		if err != nil {
			return fmt.Errorf("decode volume with alias %q: %w", alias, err)
		}

		vol.Alias = alias

		(*s)[alias] = &vol
	}

	return nil
}
