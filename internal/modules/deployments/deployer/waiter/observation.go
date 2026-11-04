// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"context"
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	swarmkitapi "github.com/moby/swarmkit/v2/api"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (w *ServiceWaiter) observeService(
	ctx context.Context, name string, baseline, service swarm.StackService, unresolved bool,
	expected dockerswarm.ServiceSpec, generation services.UpdateGeneration,
) (ServiceProgress, error) {
	if generation.ServiceID == service.ID {
		generation = services.ResolveUpdateGeneration(generation, service.PreviousSpec, service.ServiceSpec)
	}
	managed, err := services.ReplicasManaged(expected)
	if err != nil {
		return ServiceProgress{Name: name, State: ServicePending}, fmt.Errorf("service %s replicas ownership: %w", name, err)
	}
	if !managed {
		return w.observeExternalService(ctx, name, baseline, service, unresolved, expected, generation)
	}
	progress := evaluateService(name, baseline, service, nil, unresolved, expected, nil, generation)
	if service.ID == "" {
		return progress, nil
	}
	_, terminal := terminalUpdateFailure(service.UpdateStatus)
	terminal = terminal && !restoredCompletedRollback(baseline, service, expected)
	if terminal && progress.State != ServiceFailed {
		return progress, nil // The status belongs to the previous rollout.
	}
	if !terminal && service.Mode != replicatedServiceMode && service.Mode != globalServiceMode &&
		service.Mode != replicatedJobServiceMode && service.Mode != globalJobServiceMode {
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
	var placementNodes map[string]*swarmkitapi.Node
	mode := serviceVerificationMode(baseline, service, unresolved, generation)
	if mode == services.VerifyPlacementCompatibleTasks {
		nodes, listErr := w.nodes.List(ctx)
		if listErr != nil {
			progress.State = ServicePending
			progress.Message = boundedDiagnostic(fmt.Sprintf("list nodes: %v", listErr))
			return progress, fmt.Errorf("list nodes for service %s placement: %w", name, listErr)
		}
		placementNodes = placementNodesByID(nodes)
	}
	return evaluateService(name, baseline, service, tasks, unresolved, expected, placementNodes, generation), nil
}
