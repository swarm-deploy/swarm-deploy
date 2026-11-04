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
	var tasks []swarm.ServiceTask
	if service.UpdateStatus == nil && !generation.TasksAfter.IsZero() {
		// When a later write cleared rollout status, task creation (not health)
		// supplies the missing generation evidence for externally managed replicas.
		var err error
		tasks, err = w.services.ListTasks(ctx, service.ID)
		if err != nil {
			return ServiceProgress{}, err
		}
	}
	progress := specificationProgress(name, baseline, service, unresolved, expected, generation, tasks)
	if progress.State == ServicePending {
		return progress, nil
	}
	// Task health is outside specification-only verification. Diagnostics are best effort.
	if tasks == nil {
		tasks, _ = w.services.ListTasks(ctx, service.ID)
	}
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
	tasks []swarm.ServiceTask,
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
	mode := serviceVerificationMode(baseline, service, unresolved, generation)
	if ownUpdate && mode == services.VerifyNewGeneration &&
		specificationStatusPending(reference, service, unresolved, generation.ServiceID != "") {
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
		specificationRolloutPending(baseline, service, mode, unresolved, generation, tasks) {
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
	return (generation.ServiceID != "" && generation.ServiceID != service.ID) ||
		(baseline.ID == service.ID && service.UpdatedAt.Before(baseline.UpdatedAt)) ||
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
	baseline, service swarm.StackService, mode services.VerificationMode, unresolved bool,
	generation services.UpdateGeneration,
	tasks []swarm.ServiceTask,
) bool {
	// Initial creation and zero replicas need no rolling update. Externally managed
	// services still verify rollout completion, but never require healthy tasks.
	if baseline.ID == service.ID && serviceDesiredTasks(service) > 0 && mode == services.VerifyNewGeneration {
		if _, pending := verificationTaskCutoff(baseline, service, mode, unresolved, generation); pending {
			return true
		}
		if service.UpdateStatus == nil {
			return !desiredTasksFromGeneration(service, tasks, generation.TasksAfter)
		}
		if service.UpdateStatus.State != swarm.ServiceUpdateStateCompleted {
			return true
		}
	}
	return updatePending(service.UpdateStatus)
}

func desiredTasksFromGeneration(service swarm.StackService, tasks []swarm.ServiceTask, cutoff time.Time) bool {
	if cutoff.IsZero() || countDesiredTasks(tasks) != serviceDesiredTasks(service) {
		return false
	}
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning && (task.CreatedAt.Before(cutoff) ||
			!task.Spec.CompareServiceTemplate(service.ServiceSpec.TaskTemplate).Equal()) {
			return false
		}
	}
	return true
}
