package registry

import (
	"context"
	"fmt"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ImageVersion describes resolved image version details in registry.
type ImageVersion struct {
	// Image is a normalized image reference with resolved tag.
	Image string
	// Registry is a registry host where image was resolved.
	Registry string
	// Repository is an image repository path inside registry.
	Repository string
	// Tag is a resolved tag (for digest-only references can be empty).
	Tag string
	// Digest is a resolved immutable image digest.
	Digest string
}

// ImageVersionResolver resolves actual image version in registry.
type ImageVersionResolver struct {
	images      swarm.ImageManager
	authManager AuthManager
}

// NewImageVersionResolver creates image version resolver.
func NewImageVersionResolver(images swarm.ImageManager) *ImageVersionResolver {
	return &ImageVersionResolver{
		images:      images,
		authManager: NewAuthManager(),
	}
}

// ResolveActualVersion resolves actual image version in registry.
func (r *ImageVersionResolver) ResolveActualVersion(
	ctx context.Context,
	image string,
) (ImageVersion, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return ImageVersion{}, fmt.Errorf("parse image reference %q: %w", image, err)
	}
	named = reference.TagNameOnly(named)

	inspectReference := named.String()
	encodedAuth, err := r.authManager.ResolveImage(inspectReference)
	if err != nil {
		return ImageVersion{}, fmt.Errorf("resolve image auth: %w", err)
	}

	result, err := r.images.InspectDistribution(ctx, inspectReference, encodedAuth)
	if err != nil {
		return ImageVersion{}, fmt.Errorf("resolve image %q in registry: %w", inspectReference, err)
	}

	resolved := ImageVersion{
		Image:      inspectReference,
		Registry:   reference.Domain(named),
		Repository: reference.Path(named),
		Digest:     result.Descriptor.Digest.String(),
	}
	if tagged, ok := named.(reference.NamedTagged); ok {
		resolved.Tag = tagged.Tag()
	}

	return resolved, nil
}
