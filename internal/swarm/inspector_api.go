package swarm

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// InspectServiceLabels returns service, container and image labels for a stack service.
func (i *Inspector) InspectServiceLabels(
	ctx context.Context,
	stackName, serviceName, imageRef string,
) (ServiceLabels, error) {
	if i.dockerClient == nil {
		return ServiceLabels{}, errors.New("docker api client is not initialized")
	}

	fullServiceName := fmt.Sprintf("%s_%s", stackName, serviceName)
	service, _, err := i.dockerClient.ServiceInspectWithRaw(ctx, fullServiceName, dockerswarm.ServiceInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return ServiceLabels{}, ErrServiceNotFound
		}
		return ServiceLabels{}, fmt.Errorf("inspect service %s: %w", fullServiceName, err)
	}

	labels := ServiceLabels{
		Service: cloneStringMap(service.Spec.Labels),
	}
	inspectedImageRef := strings.TrimSpace(imageRef)
	if service.Spec.TaskTemplate.ContainerSpec != nil {
		labels.Container = cloneStringMap(service.Spec.TaskTemplate.ContainerSpec.Labels)
		if inspectedImageRef == "" {
			inspectedImageRef = strings.TrimSpace(service.Spec.TaskTemplate.ContainerSpec.Image)
		}
	}
	if inspectedImageRef == "" {
		return labels, nil
	}

	image, err := i.dockerClient.ImageInspect(ctx, inspectedImageRef)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return labels, nil
		}
		return labels, fmt.Errorf("inspect image %s: %w", inspectedImageRef, err)
	}

	if image.Config != nil {
		labels.Image = cloneStringMap(image.Config.Labels)
	}
	return labels, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
