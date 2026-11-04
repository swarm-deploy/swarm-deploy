// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"fmt"
	"reflect"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const waitingForUpdateStatus = "waiting for current rolling update status"

func evaluateService(
	name string,
	baseline swarm.StackService,
	service swarm.StackService,
	tasks []swarm.ServiceTask,
	unresolved bool,
	expected dockerswarm.ServiceSpec,
) ServiceProgress {
	if service.ID == "" {
		return ServiceProgress{Name: name, State: ServicePending, Message: "waiting for service to appear"}
	}

	desired := serviceDesiredTasks(service)
	progress := ServiceProgress{
		ServiceID: service.ID, Name: name, State: ServicePending, Desired: desired,
	}
	restored := restoredCompletedRollback(baseline, service, expected)
	if message, failed := terminalUpdateFailure(service.UpdateStatus); failed && !restored {
		if baseline.ID == service.ID && serviceChanged(baseline, service) &&
			reflect.DeepEqual(baseline.UpdateStatus, service.UpdateStatus) {
			progress.Message = "waiting for current update status; previous update was " + string(service.UpdateStatus.State)
			return progress
		}
		progress.State = ServiceFailed
		progress.Message = message
		progress.Message += terminalTaskDiagnostic(tasks, service.UpdateStatus.Message)
		return progress
	}
	if service.Mode == replicatedJobServiceMode || service.Mode == globalJobServiceMode {
		return jobProgress(progress, baseline, service, tasks)
	}
	if service.Mode != replicatedServiceMode && service.Mode != globalServiceMode {
		progress.State = ServiceFailed
		progress.Message = boundedDiagnostic(fmt.Sprintf("unsupported service mode %q", service.Mode))
		return progress
	}

	desired = observedDesiredTasks(service, tasks)
	progress.Desired = desired
	if globalSchedulerPending(baseline, service, desired) {
		progress.Message = "waiting for global scheduler to create tasks"
		return progress
	}
	if desired == 0 {
		return zeroDesiredProgress(progress, tasks)
	}

	// A no-op sync of the restored spec verifies its existing tasks, even when
	// an earlier failed desired spec left an unresolved verification obligation.
	unresolved = unresolved && !restored
	requireCurrentGeneration := requiresTaskGeneration(baseline, service, unresolved)
	cutoff, generationPending := verificationTaskCutoff(baseline, service, requireCurrentGeneration, unresolved)
	if generationPending {
		progress.Message = waitingForUpdateStatus
		return progress
	}

	progress.Running = countVerifiedRunningTasks(tasks, baseline, service, requireCurrentGeneration, cutoff)
	if service.UpdateStatus == nil {
		if failed, terminal := services.NonRestartingTaskFailure(service.ServiceSpec, tasks, cutoff); terminal {
			progress.State = ServiceFailed
			progress.Message = "task exited with restart disabled" + formatTaskDiagnostic(failed)
			return progress
		}
	}
	if updatePending(service.UpdateStatus) {
		progress.Message = updateProgressMessage(service.UpdateStatus)
		return progress
	}
	if progress.Running == desired && countDesiredTasks(tasks) == desired {
		progress.State = ServiceReady
		progress.Message = "service converged"
		return progress
	}

	progress.Message = boundedDiagnostic(fmt.Sprintf(
		"waiting for current tasks: %d/%d running%s",
		progress.Running,
		desired,
		taskFailureDiagnostic(tasks),
	))
	return progress
}

func restoredCompletedRollback(baseline, service swarm.StackService, expected dockerswarm.ServiceSpec) bool {
	// Only a rollback already completed before this apply may be stale. A new
	// rollback, retry generation, or a different desired spec must still fail.
	return expected.Name != "" && baseline.ID == service.ID && !serviceChanged(baseline, service) &&
		service.UpdateStatus != nil && service.UpdateStatus.State == swarm.ServiceUpdateStateRollbackCompleted &&
		reflect.DeepEqual(baseline.UpdateStatus, service.UpdateStatus) &&
		services.SpecsEqual(service.ServiceSpec, expected)
}

func serviceDesiredTasks(service swarm.StackService) uint64 {
	if service.Mode == replicatedServiceMode && service.Replicas != nil {
		return *service.Replicas
	}
	return service.DesiredTasks
}

func observedDesiredTasks(service swarm.StackService, tasks []swarm.ServiceTask) uint64 {
	desired := serviceDesiredTasks(service)
	if service.Mode != globalServiceMode {
		return desired
	}
	desiredFromTasks := countDesiredTasks(tasks)
	if desiredFromTasks > desired {
		return desiredFromTasks
	}
	return desired
}

func globalSchedulerPending(
	baseline swarm.StackService,
	service swarm.StackService,
	desired uint64,
) bool {
	if service.Mode != globalServiceMode || desired != 0 {
		return false
	}
	if baseline.ID == "" || baseline.ID != service.ID {
		return true
	}
	return baseline.DesiredTasks > 0 || !reflect.DeepEqual(
		baseline.ServiceSpec.TaskTemplate.Placement,
		service.ServiceSpec.TaskTemplate.Placement,
	)
}

func zeroDesiredProgress(progress ServiceProgress, tasks []swarm.ServiceTask) ServiceProgress {
	active := countActiveTasks(tasks)
	if active == 0 {
		progress.State = ServiceReady
		progress.Message = "service converged"
		return progress
	}
	progress.Message = boundedDiagnostic(fmt.Sprintf(
		"waiting for active tasks to stop: %d remaining%s",
		active,
		taskFailureDiagnostic(tasks),
	))
	return progress
}

func countDesiredTasks(tasks []swarm.ServiceTask) uint64 {
	var count uint64
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning {
			count++
		}
	}
	return count
}

func countActiveTasks(tasks []swarm.ServiceTask) uint64 {
	var count uint64
	for _, task := range tasks {
		if taskIsActive(task.CurrentState) {
			count++
		}
	}
	return count
}

func taskIsActive(state swarm.TaskState) bool {
	switch state {
	case swarm.TaskStateNew,
		swarm.TaskStateAllocated,
		swarm.TaskStatePending,
		swarm.TaskStateAssigned,
		swarm.TaskStateAccepted,
		swarm.TaskStatePreparing,
		swarm.TaskStateReady,
		swarm.TaskStateStarting,
		swarm.TaskStateRunning:
		return true
	case swarm.TaskStateComplete,
		swarm.TaskStateShutdown,
		swarm.TaskStateFailed,
		swarm.TaskStateRejected,
		swarm.TaskStateRemove,
		swarm.TaskStateOrphaned:
		return false
	default:
		return true
	}
}

func countCurrentRunningTasks(tasks []swarm.ServiceTask, requireCurrentGeneration bool, cutoff time.Time) uint64 {
	var count uint64
	for _, task := range tasks {
		if task.DesiredState != swarm.TaskStateRunning || task.CurrentState != swarm.TaskStateRunning {
			continue
		}
		if requireCurrentGeneration && task.CreatedAt.Before(cutoff) {
			continue
		}
		count++
	}
	return count
}

func taskGenerationCutoff(
	baseline swarm.StackService,
	service swarm.StackService,
	requireCurrentGeneration bool,
) (time.Time, bool) {
	if !requireCurrentGeneration {
		return time.Time{}, false
	}
	if baseline.ID == "" || baseline.ID != service.ID {
		return service.CreatedAt, false
	}
	if service.UpdateStatus == nil || service.UpdateStatus.StartedAt.IsZero() ||
		!service.UpdateStatus.StartedAt.After(baseline.UpdatedAt) {
		return time.Time{}, true
	}
	return service.UpdateStatus.StartedAt, false
}

func terminalUpdateFailure(status *swarm.ServiceUpdateStatus) (string, bool) {
	if status == nil {
		return "", false
	}
	switch status.State {
	case swarm.ServiceUpdateStateFailed, swarm.ServiceUpdateStatePaused, swarm.ServiceUpdateStateRollbackStarted,
		swarm.ServiceUpdateStateRollbackPaused, swarm.ServiceUpdateStateRollbackCompleted:
		message := "service update is " + string(status.State)
		if diagnostic := boundedDiagnostic(status.Message); diagnostic != "" {
			message += ": " + diagnostic
		}
		return message, true
	default:
		return "", false
	}
}

func updatePending(status *swarm.ServiceUpdateStatus) bool {
	if status == nil {
		return false
	}
	switch status.State {
	case "", swarm.ServiceUpdateStatePaused, swarm.ServiceUpdateStateRollbackStarted,
		swarm.ServiceUpdateStateRollbackPaused, swarm.ServiceUpdateStateRollbackCompleted:
		return false
	case swarm.ServiceUpdateStateUpdating:
		return true
	case swarm.ServiceUpdateStateCompleted:
		return false
	default:
		return true
	}
}

func updateProgressMessage(status *swarm.ServiceUpdateStatus) string {
	message := "waiting for rolling update to complete"
	if status != nil && boundedDiagnostic(status.Message) != "" {
		message += ": " + boundedDiagnostic(status.Message)
	}
	return message
}

func verificationTaskCutoff(
	baseline, service swarm.StackService, requireCurrentGeneration bool, unresolved bool,
) (time.Time, bool) {
	if metadataOnlyServiceUpdate(baseline, service) {
		// Metadata acceptance has no task birth cutoff; actual task templates prove convergence.
		return time.Time{}, false
	}
	if !unresolved || baseline.ID == "" || baseline.ID != service.ID ||
		!reflect.DeepEqual(baseline.ServiceSpec.TaskTemplate, service.ServiceSpec.TaskTemplate) {
		return taskGenerationCutoff(baseline, service, requireCurrentGeneration)
	}
	// Reject stale observations, including old completed statuses and their Running tasks.
	if service.UpdatedAt.Before(baseline.UpdatedAt) {
		return time.Time{}, true
	}
	// Allocation can advance UpdatedAt without a rolling update. An unchanged
	// initial spec has its creation generation, even after process recovery.
	if service.UpdateStatus == nil && baseline.UpdateStatus == nil &&
		service.PreviousSpec == nil && baseline.PreviousSpec == nil &&
		service.ServiceSpec.TaskTemplate.ForceUpdate == 0 && !service.CreatedAt.IsZero() {
		return service.CreatedAt, false
	}
	if service.UpdateStatus == nil || service.UpdateStatus.StartedAt.IsZero() {
		return time.Time{}, true
	}
	return service.UpdateStatus.StartedAt, false
}

func requiresTaskGeneration(baseline, service swarm.StackService, unresolved bool) bool {
	return unresolved || baseline.ID == "" || baseline.ID != service.ID ||
		!reflect.DeepEqual(baseline.ServiceSpec.TaskTemplate, service.ServiceSpec.TaskTemplate)
}

// metadataOnlyServiceUpdate proves that the last write preserved the task template.
// PreviousSpec alone is insufficient: a metadata write can follow an unfinished rollout,
// so managed verification must additionally compare the actual running task templates.
func metadataOnlyServiceUpdate(baseline, service swarm.StackService) bool {
	if baseline.ID == "" || baseline.ID != service.ID || service.PreviousSpec == nil ||
		service.UpdatedAt.Before(baseline.UpdatedAt) {
		return false
	}
	template := service.ServiceSpec.TaskTemplate
	return swarm.NewTaskSpec(baseline.ServiceSpec.TaskTemplate).CompareServiceTemplate(template).Equal() &&
		swarm.NewTaskSpec(service.PreviousSpec.TaskTemplate).CompareServiceTemplate(template).Equal()
}

func countVerifiedRunningTasks(
	tasks []swarm.ServiceTask, baseline, service swarm.StackService, requireCurrentGeneration bool, cutoff time.Time,
) uint64 {
	if metadataOnlyServiceUpdate(baseline, service) {
		return countMatchingRunningTasks(tasks, service.ServiceSpec.TaskTemplate)
	}
	return countCurrentRunningTasks(tasks, requireCurrentGeneration, cutoff)
}

func countMatchingRunningTasks(tasks []swarm.ServiceTask, template dockerswarm.TaskSpec) uint64 {
	var count uint64
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning && task.CurrentState == swarm.TaskStateRunning &&
			task.Spec.CompareServiceTemplate(template).Equal() {
			count++
		}
	}
	return count
}
