// Package syncpolicy evaluates deployment policies owned by GitOps synchronization.
package syncpolicy

import (
	"strings"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

const (
	// ViolationImageInvalid identifies a malformed image reference.
	ViolationImageInvalid = "image.invalid"
	// ViolationImageTagRequired identifies a missing explicit tag or digest.
	ViolationImageTagRequired = "image.tag.required"
	// ViolationImageTagNoLatest identifies an explicit or implicit latest tag.
	ViolationImageTagNoLatest = "image.tag.no_latest"
	// ViolationImageTagOnlySHA identifies an image without a digest.
	ViolationImageTagOnlySHA = "image.tag.only_sha"
)

// CheckImage returns the name of the first violated image policy.
func CheckImage(policy config.ImagePolicySpec, image string) string {
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return ViolationImageInvalid
	}

	_, hasDigest := named.(reference.Canonical)
	tagged, hasTag := named.(reference.NamedTagged)
	tag := ""
	if hasTag {
		tag = tagged.Tag()
	}

	switch {
	case policy.Tag.OnlySHA && !hasDigest:
		return ViolationImageTagOnlySHA
	case policy.Tag.Required && !hasTag && !hasDigest:
		return ViolationImageTagRequired
	case policy.Tag.NoLatest && !hasDigest && (!hasTag || tag == "latest"):
		return ViolationImageTagNoLatest
	default:
		return ""
	}
}
