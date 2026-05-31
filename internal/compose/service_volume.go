package compose

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceVolumes struct {
	Volumes []*ServiceVolume
}

type ServiceVolume struct {
	Type     string
	Source   string
	Target   string
	ReadOnly bool

	Bind *ServiceVolumeBind `yaml:"bind,omitempty"`

	isString bool
}

type ServiceVolumeBind struct {
	CreateHostPath bool   `yaml:"create_host_path,omitempty"`
	Propagation    string `yaml:"propagation,omitempty"`
}

type serviceVolumeSchema struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`

	Bind *ServiceVolumeBind `yaml:"bind,omitempty"`
}

func (sv *ServiceVolumes) UnmarshalYAML(root *yaml.Node) error {
	if root.Kind != yaml.SequenceNode {
		return fmt.Errorf("expected sequence node, got %q", root.Tag)
	}

	for _, child := range root.Content {
		volume := &ServiceVolume{}

		if child.Kind != yaml.ScalarNode && child.Kind != yaml.MappingNode {
			return fmt.Errorf("expected string or mapping node, got %q", child.Tag)
		}

		if child.Kind == yaml.ScalarNode {
			if err := volume.UnmarshalString(child.Value); err != nil {
				return fmt.Errorf("unmarshal from string: %w", err)
			}

			sv.Volumes = append(sv.Volumes, volume)
			continue
		}

		var schema serviceVolumeSchema
		if err := child.Decode(&schema); err != nil {
			return fmt.Errorf("unmarshal from mapping: %w", err)
		}

		volume.Type = schema.Type
		volume.Source = schema.Source
		volume.Target = schema.Target
		volume.ReadOnly = schema.ReadOnly
		volume.Bind = schema.Bind

		sv.Volumes = append(sv.Volumes, volume)
	}

	return nil
}

func (sv ServiceVolume) MarshalYAML() (interface{}, error) {
	if sv.isString {
		return sv.MarshalString(), nil
	}

	return &serviceVolumeSchema{
		Type:     sv.Type,
		Source:   sv.Source,
		Target:   sv.Target,
		ReadOnly: sv.ReadOnly,
		Bind:     sv.Bind,
	}, nil
}

func (sv *ServiceVolume) MarshalString() string {
	if sv.Source == "" {
		return sv.Target
	}

	buf := strings.Builder{}

	buf.WriteString(sv.Source)
	buf.WriteString(":")
	buf.WriteString(sv.Target)

	if sv.ReadOnly || (sv.Bind != nil && sv.Bind.Propagation == "rslave") {
		buf.WriteString(":")

		if sv.ReadOnly {
			buf.WriteString("ro")
		}

		if sv.Bind != nil && sv.Bind.Propagation == "rslave" {
			if sv.ReadOnly {
				buf.WriteString(",")
			}

			buf.WriteString("rslave")
		}
	}

	return buf.String()
}

func (sv *ServiceVolume) UnmarshalString(raw string) error {
	parts := strings.Split(raw, ":")
	sv.isString = true

	switch len(parts) {
	case 1:
		sv.Target = parts[0]
	case 2:
		sv.Source = parts[0]
		sv.Target = parts[1]
	case 3:
		sv.Source = parts[0]
		sv.Target = parts[1]

		modes := strings.Split(parts[2], ",")

		for _, mode := range modes {
			switch strings.ToLower(mode) {
			case "ro":
				sv.ReadOnly = true
			case "rsalve":
				if sv.Bind == nil {
					sv.Bind = &ServiceVolumeBind{
						Propagation: "rslave",
					}
				}
			}
		}
	default:
		return fmt.Errorf("invalid service volume format: %q", raw)
	}

	return nil
}
