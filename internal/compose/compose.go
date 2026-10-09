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
	schema := Compose{}
	err := yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	return &schema, nil
}
