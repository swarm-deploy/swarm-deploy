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
	name string,
	baseline swarm.StackService,
	service swarm.StackService,
	tasks []swarm.ServiceTask,
	unresolved bool,
	expected dockerswarm.ServiceSpec,
	placementNodes map[string]*swarmkitapi.Node,
) ServiceProgress {
	if service.ID == "" {
		return ServiceProgress{Name: name, State: ServicePending, Message: "waiting for service to appear"}
	}

	desired := serviceDesiredTasks(service)
	progress := ServiceProgress{
		ServiceID: service.ID, Name: name, State: ServicePending, Desired: desired,
	}
	restored := restoredCompletedRollback(baseline, service, expected)
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
	if unresolved && placementOnlyTaskPreservation(baseline, service, unresolved) &&
		expected.Name != "" && !services.SpecsEqual(service.ServiceSpec, expected) {
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
	unresolved = unresolved && !restored
	preservePlacementTasks := placementOnlyTaskPreservation(baseline, service, unresolved)
	requireCurrentGeneration := requiresTaskGeneration(baseline, service, unresolved, preservePlacementTasks)
	cutoff, generationPending := verificationTaskCutoff(baseline, service, requireCurrentGeneration, unresolved)
	if generationPending {
		progress.Message = waitingForUpdateStatus
		return progress
	}

	progress.Running = countVerifiedRunningTasks(
		tasks, baseline, service, unresolved, requireCurrentGeneration, preservePlacementTasks, cutoff, placementNodes,
	)
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
		reflect.DeepEqual(baseline.UpdateStatus, service.UpdateStatus) {
		progress.Message = "waiting for current update status; previous update was " + string(service.UpdateStatus.State)
		return progress, true
	}
	progress.State = ServiceFailed
	progress.Message = message + terminalTaskDiagnostic(tasks, service.UpdateStatus.Message)
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
	if !requireCurrentGeneration {
		// SwarmKit preserves placement-compatible tasks without starting a rolling update.
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

func requiresTaskGeneration(
	baseline, service swarm.StackService,
	unresolved bool,
	preservePlacementTasks bool,
) bool {
	if baseline.ID == "" || baseline.ID != service.ID {
		return true
	}
	if preservePlacementTasks {
		return false
	}
	return unresolved || !reflect.DeepEqual(baseline.ServiceSpec.TaskTemplate, service.ServiceSpec.TaskTemplate)
}

func placementOnlyTaskTemplateChange(baseline, service swarm.StackService) bool {
	return placementOnlyTaskTemplateDifference(
		baseline.ServiceSpec.TaskTemplate,
		service.ServiceSpec.TaskTemplate,
	)
}

func placementOnlyTaskTemplateDifference(before, after dockerswarm.TaskSpec) bool {
	if reflect.DeepEqual(before.Placement, after.Placement) {
		return false
	}
	after.Placement = before.Placement
	return swarm.NewTaskSpec(before).CompareServiceTemplate(after).Equal()
}

// placementOnlyTaskPreservation classifies the accepted transition. PreviousSpec
// is only classification evidence; task templates and assigned nodes are still verified below.
func placementOnlyTaskPreservation(
	baseline, service swarm.StackService,
	unresolved bool,
) bool {
	if baseline.ID == "" || baseline.ID != service.ID || service.UpdatedAt.Before(baseline.UpdatedAt) {
		return false
	}
	if placementOnlyTaskTemplateChange(baseline, service) {
		return true
	}
	return unresolved && service.PreviousSpec != nil && placementOnlyTaskTemplateDifference(
		service.PreviousSpec.TaskTemplate,
		service.ServiceSpec.TaskTemplate,
	)
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
	tasks []swarm.ServiceTask,
	baseline, service swarm.StackService,
	unresolved bool,
	requireCurrentGeneration bool,
	preservePlacementTasks bool,
	cutoff time.Time,
	placementNodes map[string]*swarmkitapi.Node,
) uint64 {
	if metadataOnlyServiceUpdate(baseline, service) {
		return countMatchingRunningTasks(tasks, service.ServiceSpec.TaskTemplate)
	}
	if preservePlacementTasks {
		return countPlacementCompatibleRunningTasks(tasks, service.ServiceSpec.TaskTemplate, placementNodes)
	}
	if unresolved && baseline.ID == service.ID &&
		reflect.DeepEqual(baseline.ServiceSpec.TaskTemplate, service.ServiceSpec.TaskTemplate) {
		// A historical UpdateStatus supplies only a birth cutoff. The task template
		// prevents an unrelated old rollout from attributing stale tasks to this spec.
		return countMatchingCurrentRunningTasks(tasks, service.ServiceSpec.TaskTemplate, cutoff)
	}
	return countCurrentRunningTasks(tasks, requireCurrentGeneration, cutoff)
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

func placementTaskNodesRequired(
	baseline, service swarm.StackService,
	tasks []swarm.ServiceTask,
	unresolved bool,
) bool {
	if !placementOnlyTaskPreservation(baseline, service, unresolved) {
		return false
	}
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning && task.CurrentState == swarm.TaskStateRunning {
			comparison := task.Spec.CompareServiceTemplate(service.ServiceSpec.TaskTemplate)
			if comparison.Equal() || comparison.PlacementOnly() {
				return true
			}
		}
	}
	return false
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
