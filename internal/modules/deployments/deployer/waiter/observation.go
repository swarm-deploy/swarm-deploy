// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"context"
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (w *ServiceWaiter) observeService(
	ctx context.Context, name string, baseline, service swarm.StackService, unresolved bool,
	expected dockerswarm.ServiceSpec,
) (ServiceProgress, error) {
	managed, err := services.ReplicasManaged(expected)
	if err != nil {
		return ServiceProgress{Name: name, State: ServicePending}, fmt.Errorf("service %s replicas ownership: %w", name, err)
	}
	specificationOnly := !managed && expected.Mode.Replicated != nil
	progress := evaluateService(name, baseline, service, nil, unresolved, specificationOnly)
	if specificationOnly && service.ID != "" && progress.State != ServiceFailed &&
		!services.SpecsEqual(service.ServiceSpec, expected) {
		progress.State = ServicePending
		progress.Message = "waiting for expected service specification"
		return progress, nil
	}
	if service.ID == "" {
		return progress, nil
	}
	_, terminal := terminalUpdateFailure(service.UpdateStatus)
	if terminal && progress.State != ServiceFailed {
		return progress, nil // The status belongs to the previous rollout.
	}
	if !terminal && service.Mode != replicatedServiceMode && service.Mode != globalServiceMode {
		return progress, nil
	}
	tasks, err := w.services.ListTasks(ctx, service.ID)
	if err != nil {
		if terminal {
			progress.Message += "; task diagnostics unavailable"
			return progress, nil
		}
		progress.State = ServicePending
		progress.Message = boundedDiagnostic(fmt.Sprintf("list tasks: %v", err))
		return progress, fmt.Errorf("list tasks for service %s: %w", name, err)
	}
	return evaluateService(name, baseline, service, tasks, unresolved, specificationOnly), nil
}
