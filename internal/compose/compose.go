package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Compose struct {
	Services Services           `yaml:"services" json:"services"`
	Networks map[string]Network `yaml:"networks,omitempty" json:"networks"`
	Configs  SharedObjects      `yaml:"configs,omitempty" json:"configs"`
	Secrets  SharedObjects      `yaml:"secrets,omitempty" json:"secrets"`
	Volumes  Volumes            `yaml:"volumes,omitempty" json:"volumes"`
}

func Parse(raw []byte) (*Compose, error) {
	schema := Compose{}
	err := yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	return &schema, nil
}
