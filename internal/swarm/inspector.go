package swarm

import (
	"errors"
	"fmt"

	"github.com/docker/docker/client"
)

var ErrServiceNotFound = errors.New("service not found")

// Inspector reads runtime service/container status from Docker API.
type Inspector struct {
	dockerClient *client.Client
}

// ServiceStatus contains service-level resource reservations and limits.
type ServiceStatus struct {
	// Stack is a stack name.
	Stack string
	// Service is a service name without stack prefix.
	Service string
	// Image is a full image reference configured for the service.
	Image string
	// RequestedRAMBytes is a requested memory reservation in bytes.
	RequestedRAMBytes int64
	// RequestedCPUNano is a requested CPU reservation in nano-CPUs.
	RequestedCPUNano int64
	// LimitRAMBytes is a memory limit in bytes.
	LimitRAMBytes int64
	// LimitCPUNano is a CPU limit in nano-CPUs.
	LimitCPUNano int64
}

func NewInspector() (*Inspector, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker api client: %w", err)
	}

	return &Inspector{
		dockerClient: cli,
	}, nil
}
