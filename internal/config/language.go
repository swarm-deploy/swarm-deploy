package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Language identifies the installation-wide language.
type Language string

const (
	LanguageEnglish Language = "english"
	LanguageRussian Language = "russian"
)

// UnmarshalYAML accepts the supported values of system.language.
func (l *Language) UnmarshalYAML(node *yaml.Node) error {
	value := Language(node.Value)
	if node.ShortTag() != "!!str" || (value != LanguageEnglish && value != LanguageRussian) {
		return fmt.Errorf("system.language must be %q or %q", LanguageEnglish, LanguageRussian)
	}

	*l = value
	return nil
}
