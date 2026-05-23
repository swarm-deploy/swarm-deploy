package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type ObjectRef struct {
	objectRef `yaml:"-" json:"-"`

	isString bool
}

type objectRef struct {
	Source string `yaml:"source" json:"source"`
	Target string `yaml:"target" json:"target,omitempty"`
}

func (r *ObjectRef) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		r.objectRef = objectRef{
			Source: n.Value,
			Target: "/run/secrets/" + n.Value,
		}
		r.isString = true

		*r = ObjectRef{
			objectRef: objectRef{
				Source: n.Value,
				Target: "/run/secrets/" + n.Value,
			},
			isString: true,
		}

		return nil
	}

	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %s", n.Tag)
	}

	var schema objectRef

	err := n.Decode(&schema)
	if err != nil {
		return err
	}

	r.objectRef = schema

	return nil
}

func (r ObjectRef) MarshalYAML() (interface{}, error) {
	if r.isString {
		return r.Source, nil
	}

	return r.objectRef, nil
}
