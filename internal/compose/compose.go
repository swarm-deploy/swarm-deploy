package compose

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

const (
	// ConfigsField is the Compose and snapshot key of config references and definitions.
	ConfigsField = "configs"
	// SecretsField is the Compose and snapshot key of secret references and definitions.
	SecretsField = "secrets"
)

type Compose struct {
	Services Services           `yaml:"services" json:"services"`
	Networks map[string]Network `yaml:"networks,omitempty" json:"networks"`
	Configs  Configs            `yaml:"configs,omitempty" json:"configs"`
	Secrets  Secrets            `yaml:"secrets,omitempty" json:"secrets"`
	Volumes  Volumes            `yaml:"volumes,omitempty" json:"volumes"`
}

// Parse parses a Compose definition and normalizes its service commands.
func Parse(raw []byte) (*Compose, error) {
	schema, err := ParseSource(raw)
	if err != nil {
		return nil, err
	}
	if err = normalizeServiceCommands(schema.Services); err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}
	return schema, nil
}

// ParseSource parses Compose for source inspection without interpreting service
// commands. Historical revisions may contain commands that cannot be deployed.
func ParseSource(raw []byte) (*Compose, error) {
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
