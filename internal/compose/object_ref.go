package compose

import (
	"fmt"
	"os"
	"path"
	"strings"

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

	// shortForm marks scalar Compose syntax until decoding resolves the reference
	// (see normalizeServices). Resolved references never carry it.
	shortForm bool
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
		r.Target = secretsDir + n.Value
		r.shortForm = true

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
	if r.shortForm {
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

const (
	// DefaultObjectFileMode is the file mode Docker uses for config and secret files.
	DefaultObjectFileMode os.FileMode = 0o444
	// DefaultObjectOwner is the uid and gid Docker uses for config and secret files.
	DefaultObjectOwner = "0"

	// secretsDir is where Docker mounts secrets whose target is relative.
	secretsDir = "/run/secrets/"
)

// ServiceConfig resolves a config reference for a service or Init Job.
// A short-form or target-less config is mounted at /<source>.
// See resolved for the remaining normalization.
func (r ObjectRef) ServiceConfig() ObjectRef {
	if r.shortForm || r.Target == "" {
		r.Target = "/" + r.Source
	}
	return r.resolved()
}

// ServiceSecret resolves a service secret reference to the file the service
// converter mounts: a short-form or target-less secret uses its source name,
// which Docker mounts under /run/secrets/. Relative targets keep their spelling
// because it is part of the deployed ServiceSpec. See resolved for the rest.
func (r ObjectRef) ServiceSecret() ObjectRef {
	if r.shortForm || r.Target == "" {
		r.Target = r.Source
	}
	return r.resolved()
}

// InitJobSecret resolves a secret reference as Init Jobs mount it: a short-form
// reference keeps the target assigned by the parser, an empty target uses the
// source name, and the target is absolute under /run/secrets/.
// See resolved for the remaining normalization.
func (r ObjectRef) InitJobSecret() ObjectRef {
	if r.Target == "" {
		r.Target = r.Source
	}
	r.Target = SecretPath(r.Target)
	return r.resolved()
}

// MountPath returns the mount path of a resolved reference. Init Job secret
// targets are already absolute, and Docker mounts relative configs under /.
func (r ObjectRef) MountPath() string {
	if path.IsAbs(r.Target) {
		return r.Target
	}
	return path.Join("/", r.Target)
}

// FileOwnership returns the uid, gid and mode of the mounted file with Docker defaults applied.
func (r ObjectRef) FileOwnership() (string, string, os.FileMode) {
	uid, gid, mode := r.Uid, r.Gid, DefaultObjectFileMode
	if uid == "" {
		uid = DefaultObjectOwner
	}
	if gid == "" {
		gid = DefaultObjectOwner
	}
	if r.Mode != nil {
		mode = *r.Mode
	}
	return uid, gid, mode
}

// SecretPath returns the absolute path where Docker mounts a secret target.
func SecretPath(target string) string {
	if strings.HasPrefix(target, "/") {
		return target
	}
	return secretsDir + target
}

// resolved finishes the resolution of a reference whose target is explicit:
// the short form no longer matters, and Docker's default uid, gid and mode
// are omitted. The returned reference does not share Mode with r, so the
// resolution methods are idempotent and never modify their receiver.
func (r ObjectRef) resolved() ObjectRef {
	r.shortForm = false
	if r.Uid == DefaultObjectOwner {
		r.Uid = ""
	}
	if r.Gid == DefaultObjectOwner {
		r.Gid = ""
	}
	if r.Mode != nil && *r.Mode == DefaultObjectFileMode {
		r.Mode = nil
	}
	if r.Mode != nil {
		mode := *r.Mode
		r.Mode = &mode
	}
	return r
}
