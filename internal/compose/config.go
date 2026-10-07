package compose

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

const yamlMappingNodePairSize = 2

// Config describes a top-level Compose config.
type Config struct {
	// Alias is the logical Compose resource name.
	Alias string `yaml:"-"`
	// Name overrides the Docker resource name.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// File is the source file for the config.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
	// Data contains the loaded config file content. Nil means the config has no local file.
	Data []byte `yaml:"-" json:"-"`
	// TemplateDriver controls resource data templating.
	TemplateDriver string `yaml:"template_driver,omitempty" json:"template_driver,omitempty"`
	// Labels contains metadata added to the Docker resource.
	Labels Labels `yaml:"labels,omitempty" json:"labels,omitempty"`
	// External marks a resource managed outside the Compose application.
	External bool `yaml:"external,omitempty" json:"external"`
	// Extra preserves unsupported Compose extension fields.
	Extra map[string]interface{} `yaml:",inline"`
}

// Configs contains top-level Compose configs keyed by alias.
type Configs map[string]*Config

// UnmarshalYAML decodes configs and records their Compose aliases.
func (c *Configs) UnmarshalYAML(node *yaml.Node) error {
	objects, err := decodeObjectMap[Config](node, "config", func(config *Config, alias string) {
		config.Alias = alias
	})
	if err != nil {
		return err
	}

	*c = objects

	return nil
}

// Secret describes a top-level Compose secret.
type Secret struct {
	// Alias is the logical Compose resource name.
	Alias string `yaml:"-"`
	// Name overrides the Docker resource name.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// File is the source file for the secret.
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

// Secrets contains top-level Compose secrets keyed by alias.
type Secrets map[string]*Secret

// UnmarshalYAML decodes secrets and records their Compose aliases.
func (s *Secrets) UnmarshalYAML(node *yaml.Node) error {
	objects, err := decodeObjectMap[Secret](node, "secret", func(secret *Secret, alias string) {
		secret.Alias = alias
	})
	if err != nil {
		return err
	}

	*s = objects

	return nil
}

func decodeObjectMap[T any](
	node *yaml.Node,
	objectType string,
	setAlias func(object *T, alias string),
) (map[string]*T, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node, got %T", node.Kind)
	}

	objects := make(map[string]*T, len(node.Content)/yamlMappingNodePairSize)
	for i := 0; i < len(node.Content); i += yamlMappingNodePairSize {
		alias := node.Content[i].Value
		object := new(T)
		if err := node.Content[i+1].Decode(object); err != nil {
			return nil, fmt.Errorf("decode %s with key %q: %w", objectType, alias, err)
		}

		setAlias(object, alias)
		objects[alias] = object
	}

	return objects, nil
}
