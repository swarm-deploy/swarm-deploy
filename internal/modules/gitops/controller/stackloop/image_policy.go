package stackloop

import (
	"context"
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

const (
	imageViolationInvalid  = "image.invalid"
	imageViolationRequired = "image.tag.required"
	imageViolationLatest   = "image.tag.no_latest"
	imageViolationOnlySHA  = "image.tag.only_sha"
)

func (r *Reconciler) checkImagePolicy(_ context.Context, payload *pipelinePayload) error {
	issues := make([]compose.ValidationIssue, 0)
	for _, service := range payload.Desired.Compose.Services {
		code := imagePolicyViolation(r.cfg.Spec.Sync.Policy.Image, service.Image)
		if code == "" {
			continue
		}
		issues = append(issues, compose.ValidationIssue{
			ResourceType: "service", ResourceName: service.Name, Field: "image", Code: code,
			Message: imagePolicyViolationMessage(code, service.Image),
		})
	}
	if len(issues) == 0 {
		return nil
	}
	return &compose.ValidateComposeError{FilePath: payload.Desired.Path, Issues: issues}
}

func imagePolicyViolation(policy config.ImagePolicySpec, image string) string {
	if !policy.Enabled() {
		return ""
	}
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return imageViolationInvalid
	}
	_, hasDigest := named.(reference.Canonical)
	tagged, hasTag := named.(reference.NamedTagged)
	tag := ""
	if hasTag {
		tag = tagged.Tag()
	}
	switch {
	case policy.Tag.OnlySHA && !hasDigest:
		return imageViolationOnlySHA
	case policy.Tag.Required && !hasTag && !hasDigest:
		return imageViolationRequired
	case policy.Tag.NoLatest && !hasDigest && (!hasTag || tag == "latest"):
		return imageViolationLatest
	default:
		return ""
	}
}

func imagePolicyViolationMessage(code, image string) string {
	switch code {
	case imageViolationInvalid:
		return fmt.Sprintf("image reference %q is invalid", image)
	case imageViolationRequired:
		return fmt.Sprintf("image %q must have an explicit tag or digest", image)
	case imageViolationLatest:
		return fmt.Sprintf("image %q must not use the latest tag", image)
	case imageViolationOnlySHA:
		return fmt.Sprintf("image %q must use a digest", image)
	default:
		return "image policy rejected the service image"
	}
}
