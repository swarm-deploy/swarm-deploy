package compose

import (
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ServiceCapabilities describes changes to the service container's Linux capabilities.
type ServiceCapabilities struct {
	// CapAdd lists Linux capabilities added to the service containers.
	CapAdd []string `yaml:"cap_add,omitempty" json:"cap_add,omitempty"`
	// CapDrop lists Linux capabilities removed from the service containers.
	CapDrop []string `yaml:"cap_drop,omitempty" json:"cap_drop,omitempty"`
}

type serviceCapabilityFields ServiceCapabilities

// UnmarshalYAML decodes the flat capability fields and normalizes them together.
func (c *ServiceCapabilities) UnmarshalYAML(node *yaml.Node) error {
	fields := serviceCapabilityFields(*c)
	if err := node.Decode(&fields); err != nil {
		return err
	}
	*c = ServiceCapabilities(fields).normalize()
	return nil
}

// normalize returns canonical capabilities, with additions taking precedence over matching removals.
func (c ServiceCapabilities) normalize() ServiceCapabilities {
	added := normalizeCapabilityList(c.CapAdd)
	dropped := normalizeCapabilityList(c.CapDrop)
	dropped = slices.DeleteFunc(dropped, func(value string) bool { return slices.Contains(added, value) })
	return ServiceCapabilities{CapAdd: added, CapDrop: dropped}
}

func normalizeCapabilityList(values []string) []string {
	var result []string
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "RESET" {
			continue
		}
		if value == "ALL" {
			return []string{"ALL"}
		}
		if !strings.HasPrefix(value, "CAP_") {
			value = "CAP_" + value
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result)
}
