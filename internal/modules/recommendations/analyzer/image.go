package analyzer

import (
	"context"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

const latestImageTag = "latest"

// ImageAnalyzer recommends safer image reference pinning for Compose services.
type ImageAnalyzer struct {
	now func() time.Time
}

// NewImageAnalyzer creates an image recommendation analyzer.
func NewImageAnalyzer() *ImageAnalyzer {
	return &ImageAnalyzer{
		now: time.Now,
	}
}

// Analyze inspects service image references in a Compose stack.
func (a *ImageAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Definition.Compose.Services {
		rec := a.analyze(service)
		if rec == nil {
			continue
		}

		rec.Subject = model.Subject{
			Stack:   stack.Name,
			Service: service.Name,
		}
		rec.CreatedAt = a.now()
		rec.Source = model.SourceFromStack(stack)
		recs = append(recs, *rec)
	}

	return recs
}

func (a *ImageAnalyzer) analyze(srv compose.Service) *model.Recommendation {
	ref, ok := parseImageReference(srv.Image)
	if !ok || ref.digestPinned || !ref.tagSpecified {
		return nil
	}

	if ref.tag == latestImageTag {
		return &model.Recommendation{
			Severity: model.SeverityHigh,
			Type:     model.TypeServiceImageLatest,
			Title:    "Image with latest tag",
			Recommendation: "Replace the mutable latest image tag with a specific version or digest " +
				"for reproducible deployments",
		}
	}

	return &model.Recommendation{
		Severity: model.SeverityMedium,
		Type:     model.TypeServiceImageDigestUnspecified,
		Title:    "Image with unspecified digest",
		Recommendation: "Pin the service image with @sha256:... because image tags are mutable " +
			"and can change between deployments",
	}
}

type imageReference struct {
	tagSpecified bool
	tag          string
	digestPinned bool
}

func parseImageReference(image string) (imageReference, bool) {
	trimmed := strings.TrimSpace(image)
	if trimmed == "" {
		return imageReference{}, false
	}

	named, err := reference.ParseNormalizedNamed(trimmed)
	if err != nil {
		return imageReference{}, false
	}

	ref := imageReference{}
	if _, ok := named.(reference.Canonical); ok {
		ref.digestPinned = true
	}
	if tagged, ok := named.(reference.NamedTagged); ok {
		ref.tagSpecified = true
		ref.tag = tagged.Tag()
	}

	return ref, true
}
