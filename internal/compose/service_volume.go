package compose

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/mount"
	"go.yaml.in/yaml/v3"
)

type ServiceVolumeType string

const (
	ServiceVolumeTypeBind   = "bind"
	ServiceVolumeTypeVolume = "volume"
	ServiceVolumeTypeTmpfs  = "tmpfs"
)

type ServiceVolumes struct {
	Volumes []*ServiceVolume `json:"Volumes"`

	// Map<ServiceVolume.Target, ServiceVolume>
	Map map[string]*ServiceVolume `json:"Map"`
}

type ServiceVolume struct {
	Type        ServiceVolumeType `json:"Type"`
	Source      string            `json:"Source"`
	Target      string            `json:"Target"`
	Consistency mount.Consistency `json:"Consistency"`
	ReadOnly    bool              `json:"ReadOnly"`

	Bind   *ServiceVolumeBind   `yaml:"bind,omitempty" json:"Bind"`
	Volume *ServiceVolumeVolume `yaml:"volume,omitempty" json:"Volume"`
	Tmpfs  *ServiceVolumeTmpfs  `yaml:"tmpfs,omitempty" json:"Tmpfs"`

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`

	isString bool
}

type ServiceVolumeBind struct {
	CreateHostPath *bool             `yaml:"create_host_path,omitempty" json:"CreateHostPath"`
	Propagation    mount.Propagation `yaml:"propagation,omitempty" json:"Propagation"`

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`
}

type ServiceVolumeVolume struct {
	Nocopy  bool   `yaml:"nocopy" json:"Nocopy"`
	Subpath string `yaml:"subpath" json:"Subpath"`

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`
}

type ServiceVolumeTmpfs struct {
	Mode os.FileMode `yaml:"mode" json:"Mode"`
	Size string      `yaml:"size" json:"Size"`

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`
}

type serviceVolumeSchema struct {
	Type        ServiceVolumeType `yaml:"type" json:"Type"`
	Source      string            `yaml:"source" json:"Source"`
	Target      string            `yaml:"target" json:"Target"`
	ReadOnly    bool              `yaml:"read_only" json:"ReadOnly"`
	Consistency mount.Consistency `yaml:"consistency,omitempty" json:"Consistency"`

	Bind   *ServiceVolumeBind   `yaml:"bind,omitempty" json:"Bind"`
	Volume *ServiceVolumeVolume `yaml:"volume,omitempty" json:"Volume"`
	Tmpfs  *ServiceVolumeTmpfs  `yaml:"tmpfs,omitempty" json:"Tmpfs"`

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`
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
		volume.Consistency = schema.Consistency
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
		Type:        sv.Type,
		Source:      sv.Source,
		Target:      sv.Target,
		ReadOnly:    sv.ReadOnly,
		Consistency: sv.Consistency,
		Bind:        sv.Bind,
		Volume:      sv.Volume,
		Tmpfs:       sv.Tmpfs,
		Extra:       sv.Extra,
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

			buf.WriteString(string(sv.Bind.Propagation))
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
			mode = strings.ToLower(mode)

			if mode == "ro" {
				sv.ReadOnly = true
			} else if slices.Contains(mount.Propagations, mount.Propagation(mode)) {
				sv.Bind = &ServiceVolumeBind{
					Propagation: mount.Propagation(mode),
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
