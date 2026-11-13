// Package policies validates GitOps preparation policies.
package policies

import (
	"context"
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

// Image policy violation codes are preserved in preparation failures.
const (
	ImageViolationInvalid  = "image.invalid"
	ImageViolationRequired = "image.tag.required"
	ImageViolationLatest   = "image.tag.no_latest"
	ImageViolationOnlySHA  = "image.tag.only_sha"
)

// ImageChecker checks service and init-job images against the configured policy.
type ImageChecker struct{ policy config.ImagePolicySpec }

// NewImageChecker creates an image policy checker.
func NewImageChecker(policy config.ImagePolicySpec) *ImageChecker {
	return &ImageChecker{policy: policy}
}

// Check returns policy violations in service and init-job order.
func (c *ImageChecker) Check(_ context.Context, service compose.Service) []compose.ValidationIssue {
	var issues []compose.ValidationIssue
	if code := c.violation(service.Image); code != "" {
		issues = append(issues, compose.ValidationIssue{ResourceType: "service", ResourceName: service.Name,
			Field: "image", Code: code, Message: violationMessage(code)})
	}
	for _, job := range service.InitJobs {
		if code := c.violation(job.Image); code != "" {
			issues = append(issues, compose.ValidationIssue{ResourceType: "service", ResourceName: service.Name,
				Field: fmt.Sprintf("x-init-deploy-jobs[%q].image", job.Name), Code: code,
				Message: fmt.Sprintf("init job %q: %s", job.Name, violationMessage(code))})
		}
	}
	return issues
}

func (c *ImageChecker) violation(image string) string {
	if !c.policy.Enabled() {
		return ""
	}
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return ImageViolationInvalid
	}
	_, hasDigest := named.(reference.Canonical)
	tagged, hasTag := named.(reference.NamedTagged)
	tag := ""
	if hasTag {
		tag = tagged.Tag()
	}
	switch {
	case c.policy.Tag.OnlySHA && !hasDigest:
		return ImageViolationOnlySHA
	case c.policy.Tag.Required && !hasTag && !hasDigest:
		return ImageViolationRequired
	case c.policy.Tag.NoLatest && ((hasTag && tag == "latest") || (!hasTag && !hasDigest)):
		return ImageViolationLatest
	default:
		return ""
	}
}

func violationMessage(code string) string {
	switch code {
	case ImageViolationInvalid:
		return "image reference is invalid"
	case ImageViolationRequired:
		return "image must have an explicit tag or digest"
	case ImageViolationLatest:
		return "image must not use the latest tag"
	case ImageViolationOnlySHA:
		return "image must use a digest"
	default:
		return "image policy rejected the service image"
	}
}
