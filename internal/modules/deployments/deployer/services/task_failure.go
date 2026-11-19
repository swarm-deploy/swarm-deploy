package services

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/moby/swarmkit/v2/manager/constraint"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// NonRestartingTaskFailure finds a failed or rejected current-generation replica that cannot
// be replaced under an explicit restart_policy.condition=none. Pending/running
// replacements and failures outside the desired slots never establish failure.
func NonRestartingTaskFailure(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, since time.Time,
) (swarm.ServiceTask, bool) {
	if spec.Mode.Replicated == nil || spec.TaskTemplate.RestartPolicy == nil ||
		spec.TaskTemplate.RestartPolicy.Condition != dockerswarm.RestartPolicyConditionNone {
		return swarm.ServiceTask{}, false
	}
	replicas := uint64(1)
	if spec.Mode.Replicated.Replicas != nil {
		replicas = *spec.Mode.Replicated.Replicas
	}
	latest := make(map[int]swarm.ServiceTask)
	for _, task := range tasks {
		if task.Slot <= 0 || task.CreatedAt.Before(since) {
			continue
		}
		if uint64(task.Slot) > replicas {
			continue
		}
		previous, exists := latest[task.Slot]
		if !exists || task.CreatedAt.After(previous.CreatedAt) {
			latest[task.Slot] = task
		}
	}
	var failed swarm.ServiceTask
	for _, task := range latest {
		terminalFailure := task.CurrentState == swarm.TaskStateRejected ||
			(task.CurrentState == swarm.TaskStateFailed && (task.ExitCode == nil || *task.ExitCode != 0))
		if terminalFailure && (failed.ID == "" || task.UpdatedAt.After(failed.UpdatedAt)) {
			failed = task
		}
	}
	return failed, failed.ID != ""
}

// TerminalServiceTaskFailure finds an unreplaced terminal task failure for
// which Swarm has exhausted a finite restart policy or restarts are disabled.
func TerminalServiceTaskFailure(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, since time.Time,
) (swarm.ServiceTask, bool) {
	policy := spec.TaskTemplate.RestartPolicy
	if policy == nil || (spec.Mode.Replicated == nil && spec.Mode.Global == nil) {
		return swarm.ServiceTask{}, false
	}
	if policy.Condition != dockerswarm.RestartPolicyConditionNone &&
		(policy.MaxAttempts == nil || *policy.MaxAttempts == 0) {
		return swarm.ServiceTask{}, false
	}
	var failed swarm.ServiceTask
	for _, history := range serviceTaskHistories(spec, tasks, since, false) {
		task, exhausted := terminalExhaustedTask(history, policy)
		if exhausted && (failed.ID == "" || task.UpdatedAt.After(failed.UpdatedAt)) {
			failed = task
		}
	}
	return failed, failed.ID != ""
}

// UnreplacedServiceTaskFailures returns the current failed task for each desired
// slot or node, newest failure first, even while Swarm can still retry it.
func UnreplacedServiceTaskFailures(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, since time.Time,
) []swarm.ServiceTask {
	var failed []swarm.ServiceTask
	for _, history := range serviceTaskHistories(spec, tasks, since, false) {
		task := latestServiceTask(history)
		if task.CurrentState != swarm.TaskStateFailed && task.CurrentState != swarm.TaskStateRejected &&
			task.CurrentState != swarm.TaskStateOrphaned {
			continue
		}
		failed = append(failed, task)
	}
	sortTaskFailures(failed)
	return failed
}

func sortTaskFailures(failed []swarm.ServiceTask) {
	sort.Slice(failed, func(i, j int) bool {
		if failed[i].UpdatedAt.Equal(failed[j].UpdatedAt) {
			return failed[i].ID < failed[j].ID
		}
		return failed[i].UpdatedAt.After(failed[j].UpdatedAt)
	})
}

// LatestUnrecoveredTaskAttempts returns the latest matching attempt for each desired
// slot or node without a running task, skipping replacements still in restart delay.
func LatestUnrecoveredTaskAttempts(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, since time.Time,
) []swarm.ServiceTask {
	histories := serviceTaskHistories(spec, tasks, since, true)
	attempts := make([]swarm.ServiceTask, 0, len(histories))
instances:
	for _, history := range histories {
		var latest swarm.ServiceTask
		for _, task := range history {
			if task.DesiredState == swarm.TaskStateRunning && task.CurrentState == swarm.TaskStateRunning {
				continue instances
			}
			if preparingReplacement(task) {
				continue
			}
			if latest.ID == "" || laterTaskAttempt(task, latest) {
				latest = task
			}
		}
		if latest.ID != "" {
			attempts = append(attempts, latest)
		}
	}
	sortTaskFailures(attempts)
	return attempts
}

func preparingReplacement(task swarm.ServiceTask) bool {
	if task.DesiredState != swarm.TaskStateReady || task.Error != "" {
		return false
	}
	// Swarm creates replacements with desired state ready during restart delay.
	// Reaching current state ready means Docker has finished preparing the image.
	switch task.CurrentState {
	case swarm.TaskStateNew, swarm.TaskStateAllocated, swarm.TaskStatePending,
		swarm.TaskStateAssigned, swarm.TaskStateAccepted, swarm.TaskStatePreparing:
		return true
	case swarm.TaskStateReady, swarm.TaskStateStarting, swarm.TaskStateRunning, swarm.TaskStateComplete,
		swarm.TaskStateShutdown, swarm.TaskStateFailed, swarm.TaskStateRejected,
		swarm.TaskStateRemove, swarm.TaskStateOrphaned:
	}
	return false
}

// SwarmKit keys replicated restart history by slot and global history by node.
func serviceTaskHistories(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, since time.Time, includeRestartingNodes bool,
) map[string][]swarm.ServiceTask {
	byInstance := make(map[string][]swarm.ServiceTask)
	desiredNodes := globalServiceTaskNodes(spec, tasks, since, includeRestartingNodes)
	replicas := uint64(1)
	if spec.Mode.Replicated != nil && spec.Mode.Replicated.Replicas != nil {
		replicas = *spec.Mode.Replicated.Replicas
	}
	for _, task := range tasks {
		if !matchesServiceRollout(task, spec.TaskTemplate, since) {
			continue
		}
		key := task.Node
		if spec.Mode.Replicated != nil {
			if task.Slot <= 0 || uint64(task.Slot) > replicas {
				continue
			}
			key = strconv.Itoa(task.Slot)
		} else if key == "" || !desiredNodes[key] {
			continue
		}
		// Keep shutdown attempts on relevant nodes so restart limits include history.
		byInstance[key] = append(byInstance[key], task)
	}
	return byInstance
}

func matchesServiceRollout(task swarm.ServiceTask, template dockerswarm.TaskSpec, since time.Time) bool {
	return !task.CreatedAt.Before(since) && task.Spec.CompareServiceTemplate(template).Equal()
}

func globalServiceTaskNodes(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, since time.Time, includeRestartingNodes bool,
) map[string]bool {
	if spec.Mode.Global == nil {
		return nil
	}
	var constraints []constraint.Constraint
	if spec.TaskTemplate.Placement != nil {
		parsed, err := constraint.Parse(spec.TaskTemplate.Placement.Constraints)
		if err != nil {
			return nil
		}
		constraints = parsed
	}
	desired := make(map[string]bool)
	for _, task := range tasks {
		if globalTaskNodeEligible(task, spec.TaskTemplate, constraints, since, includeRestartingNodes) {
			desired[task.Node] = true
		}
	}
	return desired
}

func globalTaskNodeEligible(
	task swarm.ServiceTask, template dockerswarm.TaskSpec, constraints []constraint.Constraint,
	since time.Time, includeRestartingNodes bool,
) bool {
	active := task.DesiredState == swarm.TaskStateRunning ||
		(includeRestartingNodes && task.DesiredState == swarm.TaskStateReady)
	node := task.NodeInfo
	if node == nil {
		// A current task assigned by Swarm identifies the node for pending pull diagnostics.
		return active && (len(constraints) == 0 || (includeRestartingNodes && matchesServiceRollout(task, template, since)))
	}
	if node.Availability == string(dockerswarm.NodeAvailabilityDrain) || node.Status == string(dockerswarm.NodeStateDown) {
		return false
	}
	// Swarm also shuts down exhausted tasks on eligible nodes. Check current
	// node attributes against the service constraints before keeping their history.
	eligible := active || node.Availability == string(dockerswarm.NodeAvailabilityActive) ||
		node.Availability == string(dockerswarm.NodeAvailabilityPause)
	return eligible && constraint.NodeMatches(constraints, node.ConstraintNode())
}

func terminalExhaustedTask(
	history []swarm.ServiceTask, policy *dockerswarm.RestartPolicy,
) (swarm.ServiceTask, bool) {
	latest := latestServiceTask(history)
	if !terminalServiceTask(latest) {
		return swarm.ServiceTask{}, false
	}
	first := history[0]
	for _, task := range history[1:] {
		if task.CreatedAt.Before(first.CreatedAt) {
			first = task
		}
	}
	if policy.Condition != dockerswarm.RestartPolicyConditionNone &&
		!retryLimitExhausted(history, first.ID, latest.UpdatedAt, policy) {
		return swarm.ServiceTask{}, false
	}
	return latest, true
}

func latestServiceTask(history []swarm.ServiceTask) swarm.ServiceTask {
	latest := history[0]
	for _, task := range history[1:] {
		if laterTaskAttempt(task, latest) {
			latest = task
		}
	}
	return latest
}

func laterTaskAttempt(task, latest swarm.ServiceTask) bool {
	return task.CreatedAt.After(latest.CreatedAt) ||
		(task.CreatedAt.Equal(latest.CreatedAt) && !terminalServiceTask(task))
}

func retryLimitExhausted(
	history []swarm.ServiceTask, firstID string, lastFailure time.Time, policy *dockerswarm.RestartPolicy,
) bool {
	var restarts uint64
	for _, task := range history {
		if task.ID == firstID {
			continue
		}
		if policy.Window != nil && *policy.Window > 0 &&
			(!task.CreatedAt.After(lastFailure.Add(-*policy.Window)) || !task.CreatedAt.Before(lastFailure)) {
			continue
		}
		restarts++
	}
	return restarts >= *policy.MaxAttempts
}

func terminalServiceTask(task swarm.ServiceTask) bool {
	return task.CurrentState == swarm.TaskStateRejected ||
		(task.CurrentState == swarm.TaskStateFailed && (task.ExitCode == nil || *task.ExitCode != 0))
}

func (a *Applier) retryRequired(ctx context.Context, service dockerswarm.Service, enabled bool) (bool, error) {
	if !enabled {
		return false, nil
	}
	job := service.Spec.Mode.ReplicatedJob != nil || service.Spec.Mode.GlobalJob != nil
	if !job && failedUpdate(service) {
		return true, nil
	}
	policy := service.Spec.TaskTemplate.RestartPolicy
	if (!job && service.UpdateStatus != nil && service.UpdateStatus.State != dockerswarm.UpdateStateCompleted) ||
		(job && service.JobStatus == nil) || policy == nil {
		return false, nil
	}
	if !job && policy.Condition != dockerswarm.RestartPolicyConditionNone &&
		(policy.MaxAttempts == nil || *policy.MaxAttempts == 0) {
		return false, nil
	}
	tasks, err := a.manager.ListTasks(ctx, service.ID)
	if err != nil {
		return false, fmt.Errorf("inspect service %s retry tasks: %w", service.Spec.Name, err)
	}
	if job {
		_, failed := JobTaskFailure(service.Spec, service.JobStatus, tasks)
		return failed, nil
	}
	// UpdatedAt may follow initial task creation because of allocation metadata.
	// Filter by task template generation instead of treating UpdatedAt as a cutoff.
	if service.Spec.Mode.Replicated != nil && policy.Condition == dockerswarm.RestartPolicyConditionNone {
		_, failed := NonRestartingTaskFailure(service.Spec, tasks, service.CreatedAt)
		return failed, nil
	}
	_, failed := TerminalServiceTaskFailure(service.Spec, tasks, service.CreatedAt)
	return failed, nil
}
