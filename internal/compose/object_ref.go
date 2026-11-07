package compose

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

type ObjectRef struct {
	Source string       `yaml:"source" json:"source"`
	Target string       `yaml:"target" json:"target,omitempty"`
	Mode   *os.FileMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	Gid    string       `yaml:"gid,omitempty" json:"gid,omitempty"`
	Uid    string       `yaml:"uid,omitempty" json:"uid,omitempty"`

	// File is a resolved repository-backed source path. It is runtime metadata and is not serialized.
	File string `yaml:"-" json:"-"`

	// ShortForm retains the scalar Compose syntax across JSON snapshots.
	// Configs use a different default target from long-form references.
	ShortForm bool `yaml:"-" json:"short_form,omitempty"`
}

type objectRef struct {
	Source string       `yaml:"source" json:"source"`
	Target string       `yaml:"target" json:"target,omitempty"`
	Mode   *os.FileMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	Gid    string       `yaml:"gid,omitempty" json:"gid,omitempty"`
	Uid    string       `yaml:"uid,omitempty" json:"uid,omitempty"`
}

func (r *ObjectRef) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		r.Source = n.Value
		r.Target = "/run/secrets/" + n.Value
		r.ShortForm = true

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

	r.Source = schema.Source
	r.Target = schema.Target
	r.Mode = schema.Mode
	r.Gid = schema.Gid
	r.Uid = schema.Uid

	return nil
}

func (r ObjectRef) MarshalYAML() (interface{}, error) {
	if r.ShortForm {
		return r.Source, nil
	}

	return objectRef{
		Source: r.Source,
		Target: r.Target,
		Gid:    r.Gid,
		Uid:    r.Uid,
		Mode:   r.Mode,
	}, nil
}

// IsShortForm reports whether the reference used scalar Compose syntax.
func (r ObjectRef) IsShortForm() bool { return r.ShortForm }
