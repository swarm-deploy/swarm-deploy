package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Volumes map[string]*Volume

type Volume struct {
	Alias string `yaml:"-" json:"-"`

	Name     string            `yaml:"name" json:"name"`
	External string            `yaml:"external" json:"external"`
	Labels   map[string]string `yaml:"labels" json:"labels"`

	Driver     string            `yaml:"driver" json:"driver"`
	DriverOpts map[string]string `yaml:"driver_opts" json:"driver_opts"`
}

func (s *Volumes) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

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

		vol.Name = alias

		(*s)[alias] = &vol
	}

	return nil
}
