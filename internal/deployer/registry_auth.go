package deployer

import (
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

func (r *InitJobRunner) buildInitServiceCreateOptions(image string) (dockerswarm.ServiceCreateOptions, error) {
	encodedRegistryAuth, err := r.authManager.ResolveImage(image)
	if err != nil {
		return dockerswarm.ServiceCreateOptions{}, err
	}

	if encodedRegistryAuth == "" {
		return dockerswarm.ServiceCreateOptions{}, nil
	}

	return dockerswarm.ServiceCreateOptions{
		EncodedRegistryAuth: encodedRegistryAuth,
		QueryRegistry:       true,
	}, nil
}
