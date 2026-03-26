package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/distribution/reference"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// GetActualImageVersion returns actual image version from registry.
type GetActualImageVersion struct {
	resolver ImageVersionResolver
}

// NewGetActualImageVersion creates get_actual_image_version component.
func NewGetActualImageVersion(resolver ImageVersionResolver) *GetActualImageVersion {
	return &GetActualImageVersion{
		resolver: resolver,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GetActualImageVersion) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "get_actual_image_version",
		Description: "Resolves an actual image tag and digest in container registry (Docker Hub and registry v2 compatible registries).", //nolint:lll//not need
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"image",
			},
			"properties": map[string]any{
				"image": map[string]any{
					"type":        "string",
					"description": "Docker image reference to resolve (for example: nginx, nginx:1.29, ghcr.io/org/app).",
				},
			},
		},
	}
}

// Execute runs get_actual_image_version tool.
func (g *GetActualImageVersion) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	if g.resolver == nil {
		return routing.Response{}, fmt.Errorf("image version resolver is not configured")
	}

	imageRaw, err := parseStringParam(request.Payload["image"], "image")
	if err != nil {
		return routing.Response{}, err
	}
	image := strings.TrimSpace(imageRaw)
	if image == "" {
		return routing.Response{}, fmt.Errorf("image is required")
	}

	resolvedImage, err := resolveImageForLookup(image)
	if err != nil {
		return routing.Response{}, err
	}

	version, err := g.resolver.ResolveActualVersion(ctx, resolvedImage)
	if err != nil {
		return routing.Response{}, err
	}

	payload := struct {
		// Image is a normalized image reference with resolved tag.
		Image string `json:"image"`

		// Registry is a registry host where image was resolved.
		Registry string `json:"registry"`

		// Repository is an image repository path inside registry.
		Repository string `json:"repository"`

		// Tag is a resolved tag.
		Tag string `json:"tag,omitempty"`

		// Digest is a resolved immutable image digest.
		Digest string `json:"digest"`
	}{
		Image:      version.Image,
		Registry:   version.Registry,
		Repository: version.Repository,
		Tag:        version.Tag,
		Digest:     version.Digest,
	}

	return routing.Response{Payload: payload}, nil
}

func resolveImageForLookup(image string) (string, error) {
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return "", fmt.Errorf("parse image reference %q: %w", image, err)
	}

	switch reference.Domain(named) {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		return normalizeDockerHubImageReference(image)
	default:
		return named.String(), nil
	}
}

func normalizeDockerHubImageReference(image string) (string, error) {
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return "", fmt.Errorf("parse image reference %q: %w", image, err)
	}

	targetNamed, err := reference.ParseNormalizedNamed(reference.Path(named))
	if err != nil {
		return "", fmt.Errorf("normalize docker hub image reference %q: %w", image, err)
	}

	trimmedTarget := reference.TrimNamed(targetNamed)
	if canonical, ok := named.(reference.Canonical); ok {
		canonicalNamed, withDigestErr := reference.WithDigest(trimmedTarget, canonical.Digest())
		if withDigestErr != nil {
			return "", fmt.Errorf("set digest for docker hub image reference %q: %w", image, withDigestErr)
		}
		return canonicalNamed.String(), nil
	}
	if tagged, ok := named.(reference.NamedTagged); ok {
		taggedNamed, withTagErr := reference.WithTag(trimmedTarget, tagged.Tag())
		if withTagErr != nil {
			return "", fmt.Errorf("set tag for docker hub image reference %q: %w", image, withTagErr)
		}
		return taggedNamed.String(), nil
	}

	return targetNamed.String(), nil
}
