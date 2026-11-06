package services

import (
	"context"
	"fmt"
	"strconv"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
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
		if uint64(task.Slot) > replicas { //nolint:gosec // Positive int task slots always fit uint64.
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
	for _, history := range serviceTaskHistories(spec, tasks, since) {
		task, exhausted := terminalExhaustedTask(history, policy)
		if exhausted && (failed.ID == "" || task.UpdatedAt.After(failed.UpdatedAt)) {
			failed = task
		}
	}
	return failed, failed.ID != ""
}

// SwarmKit keys replicated restart history by slot and global history by node.
func serviceTaskHistories(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, since time.Time,
) map[string][]swarm.ServiceTask {
	byInstance := make(map[string][]swarm.ServiceTask)
	replicas := uint64(1)
	if spec.Mode.Replicated != nil && spec.Mode.Replicated.Replicas != nil {
		replicas = *spec.Mode.Replicated.Replicas
	}
	for _, task := range tasks {
		if task.CreatedAt.Before(since) || !task.Spec.CompareServiceTemplate(spec.TaskTemplate).Equal() {
			continue
		}
		key := task.Node
		if spec.Mode.Replicated != nil {
			if task.Slot <= 0 || uint64(task.Slot) > replicas { //nolint:gosec // Positive task slots fit uint64.
				continue
			}
			key = strconv.Itoa(task.Slot)
		} else if key == "" {
			continue
		}
		byInstance[key] = append(byInstance[key], task)
	}
	return byInstance
}

func terminalExhaustedTask(
	history []swarm.ServiceTask, policy *dockerswarm.RestartPolicy,
) (swarm.ServiceTask, bool) {
	first, latest := history[0], history[0]
	for _, task := range history[1:] {
		if task.CreatedAt.Before(first.CreatedAt) {
			first = task
		}
		if task.CreatedAt.After(latest.CreatedAt) ||
			(task.CreatedAt.Equal(latest.CreatedAt) && !terminalServiceTask(task)) {
			latest = task
		}
	}
	if !terminalServiceTask(latest) {
		return swarm.ServiceTask{}, false
	}
	if policy.Condition != dockerswarm.RestartPolicyConditionNone &&
		!retryLimitExhausted(history, first.ID, latest.UpdatedAt, policy) {
		return swarm.ServiceTask{}, false
	}
	return latest, true
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
