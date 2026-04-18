package inspector

import (
	"context"
	"errors"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

// InspectServiceReplicas returns desired replicas count for a stack service.
func (i *Inspector) InspectServiceReplicas(
	ctx context.Context,
	stackName,
	serviceName string,
) (uint64, error) {
	service, fullServiceName, err := i.inspectStackService(ctx, stackName, serviceName)
	if err != nil {
		return 0, err
	}
	if service.Spec.Mode.Replicated == nil {
		return 0, fmt.Errorf("service %s is not replicated mode", fullServiceName)
	}
	if service.Spec.Mode.Replicated.Replicas == nil {
		return 0, nil
	}

	return *service.Spec.Mode.Replicated.Replicas, nil
}

// UpdateServiceReplicas sets desired replicas count for a stack service.
func (i *Inspector) UpdateServiceReplicas(
	ctx context.Context,
	stackName,
	serviceName string,
	replicas uint64,
) error {
	if replicas == 0 {
		return errors.New("replicas must be > 0")
	}

	service, fullServiceName, err := i.inspectStackService(ctx, stackName, serviceName)
	if err != nil {
		return err
	}
	if service.Spec.Mode.Replicated == nil || service.Spec.Mode.Replicated.Replicas == nil {
		return fmt.Errorf("service %s is not replicated mode", fullServiceName)
	}

	spec := service.Spec
	spec.Mode.Replicated.Replicas = &replicas

	_, err = i.dockerClient.ServiceUpdate(ctx, service.ID, service.Version, spec, dockerswarm.ServiceUpdateOptions{})
	if err != nil {
		return fmt.Errorf("update service %s replicas to %d: %w", fullServiceName, replicas, err)
	}

	return nil
}

func (i *Inspector) inspectStackService(
	ctx context.Context,
	stackName,
	serviceName string,
) (dockerswarm.Service, string, error) {
	fullServiceName := fmt.Sprintf("%s_%s", stackName, serviceName)
	service, _, err := i.dockerClient.ServiceInspectWithRaw(ctx, fullServiceName, dockerswarm.ServiceInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return dockerswarm.Service{}, fullServiceName, ErrServiceNotFound
		}

		return dockerswarm.Service{}, fullServiceName, fmt.Errorf("inspect service %s: %w", fullServiceName, err)
	}

	return service, fullServiceName, nil
}
