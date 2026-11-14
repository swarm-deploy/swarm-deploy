package compose

import "go.yaml.in/yaml/v3"

type serviceFields Service

// UnmarshalYAML decodes the service and normalizes its inline capabilities after YAML merges.
func (s *Service) UnmarshalYAML(node *yaml.Node) error {
	decoded := *s
	if err := node.Decode((*serviceFields)(&decoded)); err != nil {
		return err
	}
	if err := decoded.Capabilities.UnmarshalYAML(node); err != nil {
		return err
	}
	*s = decoded
	return nil
}

// MarshalYAML keeps cap_add and cap_drop at the service level.
func (s Service) MarshalYAML() (any, error) {
	return struct {
		serviceFields           `yaml:",inline"`
		serviceCapabilityFields `yaml:",inline"`
	}{
		serviceFields:           serviceFields(s),
		serviceCapabilityFields: serviceCapabilityFields(s.Capabilities),
	}, nil
}
