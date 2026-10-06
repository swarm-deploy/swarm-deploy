package compose

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// SharedObject wraps configs and secrets in compose top level.
type SharedObject struct {
	// Alias is the logical Compose resource name.
	Alias string `yaml:"-"`

	// Name overrides the Docker resource name.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// File is the source file for a config or secret.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
	// Driver is the secret driver name.
	Driver string `yaml:"driver,omitempty" json:"driver,omitempty"`
	// DriverOpts contains secret driver-specific options.
	DriverOpts map[string]string `yaml:"driver_opts,omitempty" json:"driver_opts,omitempty"`
	// TemplateDriver controls resource data templating.
	TemplateDriver string `yaml:"template_driver,omitempty" json:"template_driver,omitempty"`
	// Labels contains metadata added to the Docker resource.
	Labels Labels `yaml:"labels,omitempty" json:"labels,omitempty"`
	// External marks a resource managed outside the Compose application.
	External bool `yaml:"external,omitempty" json:"external"`

	// Extra preserves unsupported Compose extension fields.
	Extra map[string]interface{} `yaml:",inline"`
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
