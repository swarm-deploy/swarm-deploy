package compose

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceVolume struct {
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
	Mode   string `yaml:"-" json:"mode,omitempty"`

	isString bool
	asObject map[string]interface{}
}

type serviceVolume struct {
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
}

func (v *ServiceVolume) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if err := v.parseStringView(node.Value); err != nil {
			return fmt.Errorf("parse short syntax %q: %w", node.Value, err)
		}

		v.isString = true
		return nil
	}

	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected string or mapping node, got %s", node.Tag)
	}

	var parsed serviceVolume
	if err := node.Decode(&parsed); err != nil {
		return fmt.Errorf("decode mapping syntax: %w", err)
	}

	asObject := map[string]interface{}{}
	if err := node.Decode(&asObject); err != nil {
		return fmt.Errorf("decode mapping object: %w", err)
	}

	v.Source = parsed.Source
	v.Target = parsed.Target
	v.asObject = asObject

	return nil
}

func (v ServiceVolume) MarshalYAML() (interface{}, error) {
	if v.isString {
		return v.toStringView(), nil
	}

	if len(v.asObject) > 0 {
		return v.asObject, nil
	}

	return serviceVolume{
		Source: v.Source,
		Target: v.Target,
	}, nil
}

func (v *ServiceVolume) parseStringView(s string) error {
	if len(s) == 0 {
		return fmt.Errorf("empty string")
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		v.Target = parts[0]
	case 2: //nolint:mnd // not need
		v.Source = parts[0]
		v.Target = parts[1]
	default:
		v.Source = parts[0]
		v.Target = parts[1]
		v.Mode = strings.Join(parts[2:], ":")
	}

	if strings.TrimSpace(v.Target) == "" {
		return fmt.Errorf("target is empty")
	}

	return nil
}

func (v ServiceVolume) toStringView() string {
	if v.Source == "" {
		return v.Target
	}
	if v.Mode == "" {
		return v.Source + ":" + v.Target
	}
	return v.Source + ":" + v.Target + ":" + v.Mode
}
