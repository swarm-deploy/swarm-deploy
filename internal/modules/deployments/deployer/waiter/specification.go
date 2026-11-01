package waiter

import (
	"context"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (w *ServiceWaiter) observeExternalService(
	ctx context.Context, name string, baseline, service swarm.StackService, unresolved bool,
	expected dockerswarm.ServiceSpec, generation services.UpdateGeneration,
) (ServiceProgress, error) {
	progress := specificationProgress(name, baseline, service, unresolved, expected, generation)
	if progress.State == ServicePending {
		return progress, nil
	}
	// Task health is outside specification-only verification. Diagnostics are best effort.
	tasks, _ := w.services.ListTasks(ctx, service.ID)
	progress.Running = countCurrentRunningTasks(tasks, false, time.Time{})
	if progress.State == ServiceFailed {
		progress.Message += terminalTaskDiagnostic(tasks, service.UpdateStatus.Message)
	}
	return progress, nil
}

// specificationProgress attributes rollout status to the force generation actually preserved by apply.
// Docker resets UpdateStatus on ServiceUpdate; the orchestrator subsequently fills it asynchronously.
func specificationProgress(
	name string, baseline, service swarm.StackService, unresolved bool,
	expected dockerswarm.ServiceSpec, generation services.UpdateGeneration,
) ServiceProgress {
	progress := ServiceProgress{ServiceID: service.ID, Name: name, State: ServicePending,
		Desired: serviceDesiredTasks(service), SpecificationOnly: true}
	if service.ID == "" {
		progress.Message = "waiting for service to appear"
		return progress
	}
	reference := baseline
	if generation.ServiceID != "" {
		reference.ID = generation.ServiceID
		reference.UpdatedAt = generation.After
		reference.ServiceSpec.TaskTemplate.ForceUpdate = generation.ForceUpdate
	}
	if specificationMetadataPending(baseline, reference, service, generation) {
		progress.Message = "waiting for current service metadata"
		return progress
	}
	ownUpdate := generation.ServiceID != "" || unresolved || baseline.ID != service.ID ||
		!services.SpecsEqual(baseline.ServiceSpec, expected)
	if ownUpdate && specificationStatusPending(reference, service, unresolved, generation.ServiceID != "") {
		progress.Message = waitingForUpdateStatus
		return progress
	}
	externalExecution := service.ServiceSpec.TaskTemplate.ForceUpdate != reference.ServiceSpec.TaskTemplate.ForceUpdate
	if message, failed := terminalUpdateFailure(service.UpdateStatus); failed && ownUpdate && !externalExecution {
		progress.State = ServiceFailed
		progress.Message = message
		return progress
	}
	if !services.SpecsEqual(service.ServiceSpec, expected) {
		progress.Message = "waiting for expected service specification"
		return progress
	}
	if ownUpdate && !externalExecution &&
		specificationRolloutPending(baseline, service, expected, unresolved, generation) {
		progress.Message = waitingForUpdateStatus
		return progress
	}
	progress.State = ServiceReady
	progress.Message = "service specification accepted; task health not verified (externally managed replicas)"
	return progress
}

func specificationMetadataPending(
	baseline, reference, service swarm.StackService, generation services.UpdateGeneration,
) bool {
	return (baseline.ID == service.ID && service.UpdatedAt.Before(baseline.UpdatedAt)) ||
		(reference.ID == service.ID && service.UpdatedAt.Before(reference.UpdatedAt)) ||
		(generation.ServiceID == service.ID && !service.UpdatedAt.After(generation.After))
}

func specificationStatusPending(reference, service swarm.StackService, unresolved, applied bool) bool {
	status := service.UpdateStatus
	if status == nil {
		return false
	}
	if status.StartedAt.IsZero() || status.StartedAt.After(service.UpdatedAt) {
		return true
	}
	if reference.ID != service.ID {
		return false
	}
	if unresolved && !applied {
		return reference.UpdateStatus != nil && status.StartedAt.Before(reference.UpdateStatus.StartedAt)
	}
	return !status.StartedAt.After(reference.UpdatedAt)
}

func specificationRolloutPending(
	baseline, service swarm.StackService, expected dockerswarm.ServiceSpec, unresolved bool,
	generation services.UpdateGeneration,
) bool {
	// Inherited obligations need current rollout evidence. Idle writes can have no rollout.
	if unresolved && generation.ServiceID == "" {
		if _, pending := verificationTaskCutoff(baseline, service, true, true); pending {
			return true
		}
	}
	beforeTemplate := expected
	beforeTemplate.TaskTemplate = baseline.ServiceSpec.TaskTemplate
	if service.UpdateStatus == nil && serviceDesiredTasks(service) > 0 && !services.SpecsEqual(beforeTemplate, expected) {
		return true
	}
	return updatePending(service.UpdateStatus)
}
