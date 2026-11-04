package services

import (
	"strconv"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// JobTaskFailure finds an unreplaced terminal failure in the current job iteration
// when restarts are disabled or a finite retry limit is exhausted. Missing
// iteration metadata is not proof of failure. Replicated job slots start at
// zero; global jobs are keyed by node.
func JobTaskFailure(
	spec dockerswarm.ServiceSpec, status *dockerswarm.JobStatus, tasks []swarm.ServiceTask,
) (swarm.ServiceTask, bool) {
	if status == nil || (spec.Mode.ReplicatedJob == nil && spec.Mode.GlobalJob == nil) ||
		spec.TaskTemplate.RestartPolicy == nil {
		return swarm.ServiceTask{}, false
	}
	policy := spec.TaskTemplate.RestartPolicy
	if policy.Condition != dockerswarm.RestartPolicyConditionNone &&
		(policy.Condition != dockerswarm.RestartPolicyConditionOnFailure || policy.MaxAttempts == nil ||
			*policy.MaxAttempts == 0 || (policy.Window != nil && *policy.Window > 0)) {
		return swarm.ServiceTask{}, false
	}
	latest := make(map[string]swarm.ServiceTask)
	attempts := make(map[string]uint64)
	for _, task := range tasks {
		key, relevant := jobTaskKey(spec, status, task)
		if !relevant {
			continue
		}
		attempts[key]++
		previous, exists := latest[key]
		// Prefer replacement/completion evidence on equal creation timestamps, too.
		if !exists || task.CreatedAt.After(previous.CreatedAt) ||
			(task.CreatedAt.Equal(previous.CreatedAt) && !terminalJobTask(task)) {
			latest[key] = task
		}
	}
	var failed swarm.ServiceTask
	for key, task := range latest {
		if terminalJobTask(task) &&
			(policy.Condition == dockerswarm.RestartPolicyConditionNone || attempts[key] > *policy.MaxAttempts) &&
			(failed.ID == "" || task.UpdatedAt.After(failed.UpdatedAt)) {
			failed = task
		}
	}
	return failed, failed.ID != ""
}

func jobTaskKey(spec dockerswarm.ServiceSpec, status *dockerswarm.JobStatus, task swarm.ServiceTask) (string, bool) {
	if task.JobIteration == nil || *task.JobIteration != status.JobIteration {
		return "", false
	}
	if job := spec.Mode.ReplicatedJob; job != nil {
		total := uint64(1)
		if job.MaxConcurrent != nil {
			total = *job.MaxConcurrent
		}
		if job.TotalCompletions != nil {
			total = *job.TotalCompletions
		}
		if task.Slot < 0 || uint64(task.Slot) >= total {
			return "", false
		}
		return strconv.Itoa(task.Slot), true
	}
	return task.Node, task.Node != ""
}

func terminalJobTask(task swarm.ServiceTask) bool {
	switch task.CurrentState { //nolint:exhaustive // All other task states remain pending or successful.
	case swarm.TaskStateFailed:
		return task.ExitCode == nil || *task.ExitCode != 0
	case swarm.TaskStateRejected, swarm.TaskStateOrphaned:
		return true
	default:
		return false
	}
}
