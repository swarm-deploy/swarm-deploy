package compose

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceVolumeType string

const (
	ServiceVolumeTypeBind   = "bind"
	ServiceVolumeTypeVolume = "volume"
)

type ServiceVolumes struct {
	Volumes []*ServiceVolume

	// Map<ServiceVolume.Target, ServiceVolume>
	Map map[string]*ServiceVolume
}

type ServiceVolume struct {
	Type     ServiceVolumeType
	Source   string
	Target   string
	ReadOnly bool

	Bind   *ServiceVolumeBind `yaml:"bind,omitempty"`
	Volume *ServiceVolumeBind `yaml:"volume,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`

	isString bool
}

type ServiceVolumeBind struct {
	CreateHostPath bool   `yaml:"create_host_path,omitempty"`
	Propagation    string `yaml:"propagation,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`
}

type ServiceVolumeVolume struct {
	Nocopy bool `yaml:"nocopy"`

	Extra map[string]interface{} `yaml:",inline"`
}

type serviceVolumeSchema struct {
	Type     ServiceVolumeType `yaml:"type"`
	Source   string            `yaml:"source"`
	Target   string            `yaml:"target"`
	ReadOnly bool              `yaml:"read_only"`

	Bind   *ServiceVolumeBind `yaml:"bind,omitempty"`
	Volume *ServiceVolumeBind `yaml:"volume,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`
}

func (sv *ServiceVolumes) UnmarshalYAML(root *yaml.Node) error {
	if root.Kind != yaml.SequenceNode {
		return fmt.Errorf("expected sequence node, got %q", root.Tag)
	}

	sv.Map = make(map[string]*ServiceVolume, len(root.Content))

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
			sv.Map[volume.Target] = volume
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
		volume.Volume = schema.Volume
		volume.Extra = schema.Extra

		sv.Volumes = append(sv.Volumes, volume)
		sv.Map[volume.Target] = volume
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
		Volume:   sv.Volume,
		Extra:    sv.Extra,
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

	if sv.ReadOnly || (sv.Bind != nil && sv.Bind.Propagation != "") {
		buf.WriteString(":")

		if sv.ReadOnly {
			buf.WriteString("ro")
		}

		if sv.Bind != nil && sv.Bind.Propagation != "" {
			if sv.ReadOnly {
				buf.WriteString(",")
			}

			buf.WriteString(sv.Bind.Propagation)
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
	case 2: //nolint:mnd // volume and bind
		sv.Type = ServiceVolumeTypeVolume
		sv.Source = parts[0]
		sv.Target = parts[1]
		if strings.Contains(sv.Source, ".") || strings.Contains(sv.Source, "/") {
			sv.Type = ServiceVolumeTypeBind
		}
	case 3: //nolint:mnd // volume and bind with modes
		sv.Type = ServiceVolumeTypeVolume
		sv.Source = parts[0]
		sv.Target = parts[1]
		if strings.Contains(sv.Source, ".") || strings.Contains(sv.Source, "/") {
			sv.Type = ServiceVolumeTypeBind
		}

		for _, mode := range strings.Split(parts[2], ",") {
			switch strings.ToLower(mode) {
			case "ro":
				sv.ReadOnly = true
			case "slave", "rslave", "shared", "rshared", "private", "rprivate":
				if sv.Bind == nil {
					sv.Bind = &ServiceVolumeBind{
						Propagation: mode,
					}
				}
			}
		}
	default:
		return fmt.Errorf("invalid service volume format: %q", raw)
	}

	return nil
}

func (sv ServiceVolumes) MarshalYAML() (interface{}, error) {
	return sv.Volumes, nil
}
