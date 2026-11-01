package compose

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// StringList accepts either a scalar or a list of strings.
type StringList []string

// UnmarshalYAML decodes the two Compose string-list forms.
func (s *StringList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var value string
		if err := node.Decode(&value); err != nil {
			return err
		}
		*s = []string{value}
		return nil
	}
	var values []string
	if err := node.Decode(&values); err != nil {
		return err
	}
	*s = values
	return nil
}

// StringMap accepts Compose mapping and key=value list syntax.
type StringMap map[string]string

// UnmarshalYAML decodes a mapping or list, including extra_hosts host:ip syntax.
func (s *StringMap) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		var values map[string]string
		if err := node.Decode(&values); err != nil {
			return err
		}
		*s = values
		return nil
	}
	var entries []string
	if err := node.Decode(&entries); err != nil {
		return err
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			key, value, ok = strings.Cut(entry, ":")
		}
		if !ok || key == "" {
			return fmt.Errorf("expected key=value or host:ip")
		}
		values[key] = value
	}
	*s = values
	return nil
}

// Ulimit describes scalar or soft/hard resource limits.
type Ulimit struct {
	// Soft is the soft limit.
	Soft int64 `yaml:"soft" json:"soft"`
	// Hard is the hard limit.
	Hard int64 `yaml:"hard" json:"hard"`
	// Extra retains unsupported fields for validation.
	Extra map[string]interface{} `yaml:",inline" json:"Extra,omitempty"`
}

// UnmarshalYAML accepts a single limit or separate soft/hard limits.
func (u *Ulimit) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if err := node.Decode(&u.Soft); err != nil {
			return err
		}
		u.Hard = u.Soft
		return nil
	}
	type plain Ulimit
	return node.Decode((*plain)(u))
}

// CredentialSpec selects one Windows managed service account credential source.
type CredentialSpec struct {
	// Config is a Compose config alias.
	Config string `yaml:"config,omitempty" json:"config,omitempty"`
	// File names a credential spec file on the Docker node.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
	// Registry names a Windows registry credential spec.
	Registry string `yaml:"registry,omitempty" json:"registry,omitempty"`
	// Extra retains unsupported fields for validation.
	Extra map[string]interface{} `yaml:",inline" json:"Extra,omitempty"`
}

// GenericResource describes a discrete Swarm resource reservation.
type GenericResource struct {
	// DiscreteResourceSpec specifies an integer resource count.
	DiscreteResourceSpec *DiscreteResource `yaml:"discrete_resource_spec" json:"discrete_resource_spec"`
	// Extra retains unsupported fields for validation.
	Extra map[string]interface{} `yaml:",inline" json:"Extra,omitempty"`
}

// DiscreteResource specifies the kind and count of a generic resource.
type DiscreteResource struct {
	// Kind identifies the resource.
	Kind string `yaml:"kind" json:"kind"`
	// Value is the nonnegative number to reserve.
	Value int64 `yaml:"value" json:"value"`
	// Extra retains unsupported fields for validation.
	Extra map[string]interface{} `yaml:",inline" json:"Extra,omitempty"`
}

// HostsList preserves extra_hosts list order and accepts mapping syntax.
type HostsList []string

// UnmarshalYAML decodes host:ip or host=ip entries without losing duplicate hosts.
func (h *HostsList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		var values []string
		if err := node.Decode(&values); err != nil {
			return err
		}
		*h = values
		return nil
	}
	var values map[string]string
	if err := node.Decode(&values); err != nil {
		return err
	}
	for _, host := range slices.Sorted(maps.Keys(values)) {
		*h = append(*h, host+"="+values[host])
	}
	return nil
}
