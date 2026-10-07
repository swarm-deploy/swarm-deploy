package swarm

import (
	"context"
	"fmt"

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
			return Image{}, ErrImageNotFound
		}

		return Image{}, fmt.Errorf("inspect image %s: %w", imageRef, err)
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
