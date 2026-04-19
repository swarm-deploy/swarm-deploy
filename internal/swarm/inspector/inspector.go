package inspector

import (
	"github.com/docker/docker/client"
)

// Inspector reads runtime service/container status from Docker API.
type Inspector struct {
	dockerClient *client.Client
}

// New creates swarm inspector with provided docker API client.
func New(dockerClient *client.Client) *Inspector {
	return &Inspector{
		dockerClient: dockerClient,
	}
}
