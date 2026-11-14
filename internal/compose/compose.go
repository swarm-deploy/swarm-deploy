package compose

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

type Compose struct {
	Services Services           `yaml:"services" json:"services"`
	Networks map[string]Network `yaml:"networks,omitempty" json:"networks"`
	Configs  Configs            `yaml:"configs,omitempty" json:"configs"`
	Secrets  Secrets            `yaml:"secrets,omitempty" json:"secrets"`
	Volumes  Volumes            `yaml:"volumes,omitempty" json:"volumes"`
}

func Parse(raw []byte) (*Compose, error) {
	var syntax yaml.Node
	if err := yaml.Unmarshal(raw, &syntax); err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}
	ignored, err := validateComposeSyntax(&syntax)
	if err != nil {
		return nil, fmt.Errorf("validate compose: %w", err)
	}
	var schema Compose
	if decodeErr := syntax.Decode(&schema); decodeErr != nil {
		return nil, fmt.Errorf("decode compose schema: %w", decodeErr)
	}
	setIgnoredOptions(&schema, ignored)
	return &schema, nil
}
