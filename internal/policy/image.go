package policy

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
)

// ImageTagPolicySpec contains restrictions for image references.
type ImageTagPolicySpec struct {
	// Required requires an explicit tag or digest.
	Required bool `yaml:"required"`
	// NoLatest rejects the latest tag and untagged image references.
	NoLatest bool `yaml:"no_latest"`
	// OnlySHA requires a digest reference.
	OnlySHA bool `yaml:"only_sha"`
}

// ImagePolicySpec contains image policy restrictions.
type ImagePolicySpec struct {
	// Tag contains image tag restrictions.
	Tag ImageTagPolicySpec `yaml:"tag"`
}

// CheckImage returns the name of the first violated image policy.
func (p ImagePolicySpec) CheckImage(image string) string {
	image = strings.TrimSpace(image)
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		if p.Tag.Required || p.Tag.NoLatest || p.Tag.OnlySHA {
			return "image.tag.required"
		}
		return ""
	}

	_, hasDigest := named.(reference.Canonical)
	_, hasTag := named.(reference.NamedTagged)
	tag := ""
	if tagged, ok := named.(reference.NamedTagged); ok {
		tag = tagged.Tag()
	}

	switch {
	case p.Tag.OnlySHA && !hasDigest:
		return "image.tag.only_sha"
	case p.Tag.Required && !hasTag && !hasDigest:
		return "image.tag.required"
	case p.Tag.NoLatest && !hasDigest && (!hasTag || tag == "latest"):
		return "image.tag.no_latest"
	default:
		return ""
	}
}

// ValidateImage returns an error when an image violates the configured policy.
func (p ImagePolicySpec) ValidateImage(image string) error {
	if violated := p.CheckImage(image); violated != "" {
		return fmt.Errorf("%s policy violated", violated)
	}
	return nil
}
