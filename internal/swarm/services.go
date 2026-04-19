package swarm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/avast/retry-go/v5"
	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

// ErrServiceNotFound means that service does not exist in swarm.
var ErrServiceNotFound = errors.New("service not found")

// ServiceManager manages stack service replicas.
type ServiceManager struct {
	dockerClient *client.Client
}

// newServiceManager creates service manager with provided docker API client.
func newServiceManager(dockerClient *client.Client) *ServiceManager {
	return &ServiceManager{
		dockerClient: dockerClient,
	}
}

// GetReplicas returns desired replicas count for a stack service.
func (m *ServiceManager) GetReplicas(
	ctx context.Context,
	serviceRef ServiceReference,
) (uint64, error) {
	service, fullServiceName, err := m.inspect(ctx, serviceRef)
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

// Scale sets desired replicas count for a stack service.
func (m *ServiceManager) Scale(
	ctx context.Context,
	serviceRef ServiceReference,
	replicas uint64,
) error {
	service, fullServiceName, err := m.inspect(ctx, serviceRef)
	if err != nil {
		return err
	}
	if service.Spec.Mode.Replicated == nil || service.Spec.Mode.Replicated.Replicas == nil {
		return fmt.Errorf("service %s is not replicated mode", fullServiceName)
	}

	spec := service.Spec
	spec.Mode.Replicated.Replicas = &replicas

	_, err = m.dockerClient.ServiceUpdate(ctx, service.ID, service.Version, spec, dockerswarm.ServiceUpdateOptions{})
	if err != nil {
		return fmt.Errorf("update service %s replicas to %d: %w", fullServiceName, replicas, err)
	}

	return nil
}

const (
	restartServiceRetryAttempts = 3
	restartServiceDelay         = 250 * time.Second
)

// Restart restarts stack service by scaling replicas to zero and restoring previous count.
func (m *ServiceManager) Restart(
	ctx context.Context,
	serviceRef ServiceReference,
) (uint64, error) {
	currentReplicas, err := m.GetReplicas(ctx, serviceRef)
	if err != nil {
		return 0, fmt.Errorf("inspect service replicas: %w", err)
	}

	err = m.Scale(ctx, serviceRef, 0)
	if err != nil {
		return 0, fmt.Errorf("scale service replicas to 0: %w", err)
	}

	err = retry.New(retry.Attempts(restartServiceRetryAttempts), retry.Delay(restartServiceDelay)).Do(func() error {
		return m.Scale(ctx, serviceRef, currentReplicas)
	})
	if err != nil {
		return 0, fmt.Errorf("restore service replicas to %d: %w", currentReplicas, err)
	}

	return currentReplicas, nil
}

func (m *ServiceManager) inspect(
	ctx context.Context,
	serviceRef ServiceReference,
) (dockerswarm.Service, string, error) {
	fullServiceName := serviceRef.Name()
	service, _, err := m.dockerClient.ServiceInspectWithRaw(ctx, fullServiceName, dockerswarm.ServiceInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return dockerswarm.Service{}, fullServiceName, ErrServiceNotFound
		}

		return dockerswarm.Service{}, fullServiceName, fmt.Errorf("inspect service %s: %w", fullServiceName, err)
	}

	return service, fullServiceName, nil
}
