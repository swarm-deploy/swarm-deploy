// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	swarmkitapi "github.com/moby/swarmkit/v2/api"
	"github.com/moby/swarmkit/v2/manager/constraint"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	waitingForUpdateStatus      = "waiting for current rolling update status"
	waitingForGlobalReplacement = "waiting for global replacement tasks to start"
)

func evaluateService(
	name string, baseline, service swarm.StackService, tasks []swarm.ServiceTask, unresolved bool,
	expected dockerswarm.ServiceSpec, placementNodes map[string]*swarmkitapi.Node, generation services.UpdateGeneration,
) ServiceProgress {
	if service.ID == "" {
		return ServiceProgress{Name: name, State: ServicePending, Message: "waiting for service to appear"}
	}

	desired := serviceDesiredTasks(service)
	progress := ServiceProgress{
		ServiceID: service.ID, Name: name, State: ServicePending, Desired: desired,
	}
	restored := restoredCompletedRollback(baseline, service, expected)
	if restored {
		generation = services.UpdateGeneration{}
	}
	reference := baseline
	if generation.ServiceID != "" {
		reference.ID = generation.ServiceID
		reference.UpdatedAt = generation.After
	}
	if specificationMetadataPending(baseline, reference, service, generation) {
		progress.Message = "waiting for current service metadata"
		return progress
	}
	if generation.ServiceID == service.ID && !generation.After.IsZero() {
		// The persisted pre-write boundary survives a baseline captured after apply.
		baseline.UpdatedAt = generation.After
	}

	if updateProgress, terminal := terminalServiceUpdateProgress(baseline, service, tasks, restored, progress); terminal {
		return updateProgress
	}
	if expected.Name != "" && !services.SpecsEqual(service.ServiceSpec, expected) {
		progress.Message = "waiting for expected service specification"
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
	unresolved = unresolved && !restored
	mode := serviceVerificationMode(baseline, service, unresolved, generation)
	desired = observedDesiredTasks(service, tasks)
	progress.Desired = desired
	// A no-op sync of the restored spec verifies its existing tasks, even when
	// an earlier failed desired spec left an unresolved verification obligation.
	cutoff, generationPending := verificationTaskCutoff(baseline, service, mode, unresolved, generation)
	matchesTask := verificationTaskMatcher(service.ServiceSpec.TaskTemplate, mode, cutoff, placementNodes)
	replacementPending := service.Mode == globalServiceMode && globalReplacementPending(tasks, matchesTask)
	if desired == 0 && (service.Mode != globalServiceMode || generationPending) {
		return zeroDesiredProgress(progress, baseline, service, tasks, replacementPending)
	}

	if generationPending {
		progress.Message = waitingForUpdateStatus
		return progress
	}

	return runningServiceProgress(progress, baseline, service, tasks, cutoff, matchesTask, replacementPending)
}

func runningServiceProgress(
	progress ServiceProgress, baseline, service swarm.StackService, tasks []swarm.ServiceTask,
	cutoff time.Time, matchesTask func(swarm.ServiceTask) bool, replacementPending bool,
) ServiceProgress {
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning && task.CurrentState == swarm.TaskStateRunning && matchesTask(task) {
			progress.Running++
		}
	}
	if failed, terminal := services.TerminalServiceTaskFailure(service.ServiceSpec, tasks, cutoff); terminal {
		progress.failTask(failed, "task failed with no restarts remaining")
		return progress
	}
	if progress.Desired == 0 {
		// Swarm's global desired count omits exhausted tasks and replacements in restart delay.
		progress = zeroDesiredProgress(progress, baseline, service, tasks, replacementPending)
		if progress.State == ServicePending {
			attempts := services.LatestUnrecoveredTaskAttempts(service.ServiceSpec, tasks, cutoff, matchesTask)
			progress.pendingRegistryPullFaults(attempts, nil)
		}
		return progress
	}
	updating := updatePending(service.UpdateStatus)
	allRunning := !updating && progress.Running == progress.Desired && countDesiredTasks(tasks) == progress.Desired
	if allRunning && !replacementPending {
		progress.State = ServiceReady
		progress.Message = "service converged"
		return progress
	}

	progress.pendingSchedulingFault(tasks, service.ServiceSpec.TaskTemplate, cutoff)
	failed := progress.pendingFailureFaults(service.ServiceSpec, tasks, cutoff, matchesTask)
	if updating {
		progress.Message = updateProgressMessage(service.UpdateStatus)
		return progress
	}
	if allRunning {
		progress.Message = waitingForGlobalReplacement
		return progress
	}
	progress.Message = boundedDiagnostic(fmt.Sprintf(
		"waiting for current tasks: %d/%d running%s",
		progress.Running,
		progress.Desired,
		formatTaskDiagnostic(failed),
	))
	return progress
}

func globalReplacementPending(tasks []swarm.ServiceTask, matchesTask func(swarm.ServiceTask) bool) bool {
	// Swarm's desired count omits replacements in restart delay.
	return slices.ContainsFunc(tasks, func(task swarm.ServiceTask) bool {
		return task.DesiredState == swarm.TaskStateReady && matchesTask(task)
	})
}

func terminalServiceUpdateProgress(
	baseline, service swarm.StackService,
	tasks []swarm.ServiceTask,
	restored bool,
	progress ServiceProgress,
) (ServiceProgress, bool) {
	message, failed := terminalUpdateFailure(service.UpdateStatus)
	if !failed || restored {
		return progress, false
	}
	if baseline.ID == service.ID && serviceChanged(baseline, service) &&
		reflect.DeepEqual(baseline.UpdateStatus, service.UpdateStatus) &&
		!service.UpdateStatus.StartedAt.After(baseline.UpdatedAt) {
		progress.Message = "waiting for current update status; previous update was " + string(service.UpdateStatus.State)
		return progress, true
	}
	task := terminalFailureTask(tasks, service.UpdateStatus.Message, terminalTaskCutoff(baseline, service))
	progress.failTask(task, message)
	return progress, true
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

func zeroDesiredProgress(
	progress ServiceProgress, baseline, service swarm.StackService, tasks []swarm.ServiceTask, replacementPending bool,
) ServiceProgress {
	if service.Mode == globalServiceMode && (baseline.ID != service.ID || baseline.DesiredTasks > 0 ||
		!reflect.DeepEqual(baseline.ServiceSpec.TaskTemplate.Placement, service.ServiceSpec.TaskTemplate.Placement)) {
		progress.Message = "waiting for global scheduler to create tasks"
		return progress
	}
	if replacementPending {
		progress.Message = waitingForGlobalReplacement
		return progress
	}
	active := countActiveTasks(tasks)
	if active == 0 {
		progress.State = ServiceReady
		progress.Message = "service converged"
		return progress
	}
	progress.Message = fmt.Sprintf("waiting for active tasks to stop: %d remaining", active)
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
) (time.Time, bool) {
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
	case swarm.ServiceUpdateStateUpdating, swarm.ServiceUpdateStateCompleted:
		return "", false
	default:
		return "", false
	}
}

func updatePending(status *swarm.ServiceUpdateStatus) bool {
	if status == nil {
		return false
	}
	switch status.State {
	case swarm.ServiceUpdateStatePaused, swarm.ServiceUpdateStateRollbackStarted,
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

// serviceVerificationMode retains unresolved obligations across later writes.
// Legacy receipts fall back to the accepted transition in Docker's PreviousSpec.
func serviceVerificationMode(
	baseline, service swarm.StackService, unresolved bool, generation services.UpdateGeneration,
) services.VerificationMode {
	if baseline.ID == "" || baseline.ID != service.ID || service.UpdatedAt.Before(baseline.UpdatedAt) {
		return services.VerifyNewGeneration
	}
	mode := services.ClassifyVerification(baseline.ServiceSpec, service.ServiceSpec)
	// A later non-replacing write cannot weaken an unresolved obligation.
	if generation.ServiceID == service.ID && generation.Mode > mode {
		return generation.Mode
	}
	if mode != services.VerifyExistingTasks || (!unresolved && generation.ServiceID == "") {
		return mode
	}
	if service.PreviousSpec != nil {
		return services.ClassifyVerification(*service.PreviousSpec, service.ServiceSpec)
	}
	return services.VerifyNewGeneration
}

func verificationTaskCutoff(
	baseline, service swarm.StackService, mode services.VerificationMode, unresolved bool,
	generation services.UpdateGeneration,
) (time.Time, bool) {
	if mode != services.VerifyNewGeneration {
		return time.Time{}, false
	}
	if generation.ServiceID == service.ID && !generation.After.IsZero() {
		baseline.UpdatedAt = generation.After
		if !generation.TasksAfter.IsZero() {
			// A later non-replacing write can cancel the old updater with clean
			// slots and leave no UpdateStatus. Its accepted metadata plus all tasks
			// from the original generation establish convergence in that case.
			if service.UpdateStatus == nil {
				return generation.TasksAfter, false
			}
			_, pending := taskGenerationCutoff(baseline, service)
			return generation.TasksAfter, pending
		}
		return taskGenerationCutoff(baseline, service)
	}
	if !unresolved || baseline.ID == "" || baseline.ID != service.ID ||
		services.ClassifyVerification(baseline.ServiceSpec, service.ServiceSpec) != services.VerifyExistingTasks {
		return taskGenerationCutoff(baseline, service)
	}
	if service.UpdatedAt.Before(baseline.UpdatedAt) {
		return time.Time{}, true
	}
	// An unchanged initial spec has its creation generation, including after recovery.
	if service.UpdateStatus == nil && baseline.UpdateStatus == nil &&
		service.PreviousSpec == nil && baseline.PreviousSpec == nil &&
		service.ServiceSpec.TaskTemplate.ForceUpdate == 0 && !service.CreatedAt.IsZero() {
		return service.CreatedAt, false
	}
	if service.UpdateStatus == nil || service.UpdateStatus.StartedAt.IsZero() {
		return time.Time{}, true
	}
	// Legacy unresolved endpoint writes have no durable boundary. Their unchanged
	// task templates cannot distinguish a historical rollout, so require fresh evidence.
	if service.PreviousSpec != nil && swarm.NewTaskSpec(service.PreviousSpec.TaskTemplate).
		CompareServiceTemplate(service.ServiceSpec.TaskTemplate).Equal() {
		return taskGenerationCutoff(baseline, service)
	}
	return service.UpdateStatus.StartedAt, false
}

func verificationTaskMatcher(
	template dockerswarm.TaskSpec, mode services.VerificationMode, cutoff time.Time, nodes map[string]*swarmkitapi.Node,
) func(swarm.ServiceTask) bool {
	if mode == services.VerifyNewGeneration {
		return func(task swarm.ServiceTask) bool {
			return !task.CreatedAt.Before(cutoff) && task.Spec.CompareServiceTemplate(template).Equal()
		}
	}
	if mode != services.VerifyExistingTasks && mode != services.VerifyPlacementCompatibleTasks {
		return func(swarm.ServiceTask) bool { return false }
	}
	var constraints []constraint.Constraint
	if template.Placement != nil {
		parsed, err := constraint.Parse(template.Placement.Constraints)
		if err != nil {
			return func(swarm.ServiceTask) bool { return false }
		}
		constraints = parsed
	}
	return func(task swarm.ServiceTask) bool {
		comparison := task.Spec.CompareServiceTemplate(template)
		// Metadata-only writes can verify exact matches without a node lookup.
		if comparison.Equal() && mode == services.VerifyExistingTasks {
			return true
		}
		node := nodes[task.Node]
		return (comparison.Equal() || comparison.PlacementOnly()) && node != nil && constraint.NodeMatches(constraints, node)
	}
}

func placementNodesByID(nodes []swarm.Node) map[string]*swarmkitapi.Node {
	mapped := make(map[string]*swarmkitapi.Node, len(nodes))
	for _, node := range nodes {
		mapped[node.ID] = node.ConstraintNode()
	}
	return mapped
}
