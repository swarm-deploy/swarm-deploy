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

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`
}

func Parse(raw []byte) (*Compose, error) {
	var syntax yaml.Node
	if err := yaml.Unmarshal(raw, &syntax); err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}
	interpolated, err := interpolateSyntax(&syntax)
	if err != nil {
		return nil, fmt.Errorf("interpolate compose schema: %w", err)
	}
	schema := Compose{}
	err = syntax.Decode(&schema)
	if err != nil {
		if interpolated {
			err = fmt.Errorf("invalid Compose value after interpolation")
		}
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	return &schema, nil
}
