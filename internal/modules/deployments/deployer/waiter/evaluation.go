// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	swarmkitapi "github.com/moby/swarmkit/v2/api"
	"github.com/moby/swarmkit/v2/manager/constraint"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const waitingForUpdateStatus = "waiting for current rolling update status"

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
	if mode == services.VerifyPlacementCompatibleTasks && expected.Name != "" &&
		!services.SpecsEqual(service.ServiceSpec, expected) {
		progress.Message = "waiting for expected service specification"
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
	cutoff, generationPending := verificationTaskCutoff(baseline, service, mode, unresolved, generation)
	if generationPending {
		progress.Message = waitingForUpdateStatus
		return progress
	}

	return runningServiceProgress(progress, service, tasks, mode, cutoff, placementNodes)
}

func runningServiceProgress(
	progress ServiceProgress, service swarm.StackService, tasks []swarm.ServiceTask,
	mode services.VerificationMode, cutoff time.Time, placementNodes map[string]*swarmkitapi.Node,
) ServiceProgress {
	progress.Running = countVerifiedRunningTasks(
		tasks, service.ServiceSpec.TaskTemplate, mode, cutoff, placementNodes,
	)
	if service.UpdateStatus == nil {
		if failed, terminal := services.NonRestartingTaskFailure(service.ServiceSpec, tasks, cutoff); terminal {
			progress.State = ServiceFailed
			progress.FailedTaskID = failed.ID
			progress.Message = "task exited with restart disabled" + formatTaskDiagnostic(failed)
			return progress
		}
	}
	if updatePending(service.UpdateStatus) {
		progress.Message = updateProgressMessage(service.UpdateStatus)
		return progress
	}
	if progress.Running == progress.Desired && countDesiredTasks(tasks) == progress.Desired {
		progress.State = ServiceReady
		progress.Message = "service converged"
		return progress
	}

	progress.Message = boundedDiagnostic(fmt.Sprintf(
		"waiting for current tasks: %d/%d running%s",
		progress.Running,
		progress.Desired,
		taskFailureDiagnostic(tasks),
	))
	return progress
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
	progress.State = ServiceFailed
	task := terminalFailureTask(tasks, service.UpdateStatus.Message, terminalTaskCutoff(baseline, service))
	progress.FailedTaskID = task.ID
	progress.Message = message + formatTaskDiagnostic(task)
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

func countVerifiedRunningTasks(
	tasks []swarm.ServiceTask, template dockerswarm.TaskSpec, mode services.VerificationMode,
	cutoff time.Time, nodes map[string]*swarmkitapi.Node,
) uint64 {
	switch mode {
	case services.VerifyExistingTasks:
		return countMatchingRunningTasks(tasks, template)
	case services.VerifyPlacementCompatibleTasks:
		return countPlacementCompatibleRunningTasks(tasks, template, nodes)
	case services.VerifyNewGeneration:
		return countMatchingCurrentRunningTasks(tasks, template, cutoff)
	default:
		return 0
	}
}

func countMatchingCurrentRunningTasks(
	tasks []swarm.ServiceTask,
	template dockerswarm.TaskSpec,
	cutoff time.Time,
) uint64 {
	var count uint64
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning && task.CurrentState == swarm.TaskStateRunning &&
			!task.CreatedAt.Before(cutoff) && task.Spec.CompareServiceTemplate(template).Equal() {
			count++
		}
	}
	return count
}

func countPlacementCompatibleRunningTasks(
	tasks []swarm.ServiceTask,
	template dockerswarm.TaskSpec,
	nodes map[string]*swarmkitapi.Node,
) uint64 {
	var constraints []constraint.Constraint
	if template.Placement != nil {
		parsed, err := constraint.Parse(template.Placement.Constraints)
		if err != nil {
			return 0
		}
		constraints = parsed
	}

	var count uint64
	for _, task := range tasks {
		if task.DesiredState != swarm.TaskStateRunning || task.CurrentState != swarm.TaskStateRunning {
			continue
		}
		comparison := task.Spec.CompareServiceTemplate(template)
		if (comparison.Equal() || comparison.PlacementOnly()) &&
			nodeMatchesConstraints(constraints, nodes[task.Node]) {
			count++
		}
	}
	return count
}

func nodeMatchesConstraints(constraints []constraint.Constraint, node *swarmkitapi.Node) bool {
	return node != nil && constraint.NodeMatches(constraints, node)
}

func placementNodesByID(nodes []swarm.Node) map[string]*swarmkitapi.Node {
	mapped := make(map[string]*swarmkitapi.Node, len(nodes))
	for _, node := range nodes {
		role := swarmkitapi.NodeRoleWorker
		if strings.EqualFold(node.Role, string(dockerswarm.NodeRoleManager)) {
			role = swarmkitapi.NodeRoleManager
		}
		mapped[node.ID] = &swarmkitapi.Node{
			ID:   node.ID,
			Role: role,
			Spec: swarmkitapi.NodeSpec{Annotations: swarmkitapi.Annotations{Labels: node.Labels}},
			Description: &swarmkitapi.NodeDescription{
				Hostname: node.Hostname,
				Platform: &swarmkitapi.Platform{OS: node.PlatformOS, Architecture: node.PlatformArchitecture},
				Engine:   &swarmkitapi.EngineDescription{Labels: node.EngineLabels},
			},
			Status: swarmkitapi.NodeStatus{Addr: node.Addr},
		}
	}
	return mapped
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
