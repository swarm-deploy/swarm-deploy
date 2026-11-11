package swarm

import (
	"context"
	"fmt"

	dockerregistry "github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
)

// imageManager manages Docker image metadata.
type imageManager struct {
	dockerClient *client.Client
}

// newImageManager creates image manager with provided Docker API client.
func newImageManager(dockerClient *client.Client) *imageManager {
	return &imageManager{
		dockerClient: dockerClient,
	}
}

// Get returns compact image metadata by image reference.
func (m *imageManager) Get(ctx context.Context, imageRef string) (Image, error) {
	image, err := m.dockerClient.ImageInspect(ctx, imageRef)
	if err != nil {
		if isNotFoundErr(err) {
			return Image{}, fmt.Errorf("%w: %w", ErrImageNotFound, err)
		}

		return Image{}, fmt.Errorf("inspect image %s: %w", imageRef, classifyDockerError(err))
	}

	labels := map[string]string(nil)
	if image.Config != nil {
		labels = cloneStringMap(image.Config.Labels)
	}

	return Image{
		ID:     image.ID,
		Ref:    imageRef,
		Labels: labels,
	}, nil
}

// InspectDistribution returns registry distribution metadata through Docker Engine.
func (m *imageManager) InspectDistribution(
	ctx context.Context, imageRef, encodedRegistryAuth string,
) (dockerregistry.DistributionInspect, error) {
	result, err := m.dockerClient.DistributionInspect(ctx, imageRef, encodedRegistryAuth)
	if err != nil {
		return dockerregistry.DistributionInspect{}, classifyDockerError(err)
	}

	return result, nil
}
