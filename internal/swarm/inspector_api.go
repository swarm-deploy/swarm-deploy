package swarm

import (
	"context"
	"errors"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

func (i *Inspector) InspectServiceStatus(ctx context.Context, stackName, serviceName string) (ServiceStatus, error) {
	if i.dockerClient == nil {
		return ServiceStatus{}, errors.New("docker api client is not initialized")
	}

	fullServiceName := fmt.Sprintf("%s_%s", stackName, serviceName)
	service, _, err := i.dockerClient.ServiceInspectWithRaw(ctx, fullServiceName, dockerswarm.ServiceInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return ServiceStatus{}, ErrServiceNotFound
		}
		return ServiceStatus{}, fmt.Errorf("inspect service %s: %w", fullServiceName, err)
	}

	status := ServiceStatus{
		Stack:   stackName,
		Service: serviceName,
	}
	if service.Spec.TaskTemplate.ContainerSpec != nil {
		status.Image = service.Spec.TaskTemplate.ContainerSpec.Image
	}

	if resources := service.Spec.TaskTemplate.Resources; resources != nil && resources.Reservations != nil {
		status.RequestedRAMBytes = resources.Reservations.MemoryBytes
		status.RequestedCPUNano = resources.Reservations.NanoCPUs
	}
	if resources := service.Spec.TaskTemplate.Resources; resources != nil && resources.Limits != nil {
		status.LimitRAMBytes = resources.Limits.MemoryBytes
		status.LimitCPUNano = resources.Limits.NanoCPUs
	}

	return status, nil
}
