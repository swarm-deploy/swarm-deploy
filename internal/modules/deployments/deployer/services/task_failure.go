package services

import (
	"context"
	"fmt"
	"strconv"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// TerminalServiceTaskFailure finds an unreplaced terminal task failure for
// which Swarm will not schedule another attempt. Replicated instances are keyed
// by slot, global instances by node, as in SwarmKit's restart supervisor.
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
	replicas := uint64(1)
	if spec.Mode.Replicated != nil && spec.Mode.Replicated.Replicas != nil {
		replicas = *spec.Mode.Replicated.Replicas
	}
	byInstance := make(map[string][]swarm.ServiceTask)
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
	var failed swarm.ServiceTask
	for _, history := range byInstance {
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
			continue
		}
		if policy.Condition != dockerswarm.RestartPolicyConditionNone {
			var restarts uint64
			for _, task := range history {
				if task.ID == first.ID {
					continue // The first task is not a restart.
				}
				if policy.Window != nil && *policy.Window > 0 &&
					(!task.CreatedAt.After(latest.UpdatedAt.Add(-*policy.Window)) ||
						!task.CreatedAt.Before(latest.UpdatedAt)) {
					continue
				}
				restarts++
			}
			if restarts < *policy.MaxAttempts {
				continue
			}
		}
		if failed.ID == "" || latest.UpdatedAt.After(failed.UpdatedAt) {
			failed = latest
		}
	}
	return failed, failed.ID != ""
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
	if (!job && service.UpdateStatus != nil) || (job && service.JobStatus == nil) ||
		service.Spec.TaskTemplate.RestartPolicy == nil ||
		(!job && service.Spec.TaskTemplate.RestartPolicy.Condition != dockerswarm.RestartPolicyConditionNone &&
		(service.Spec.TaskTemplate.RestartPolicy.MaxAttempts == nil ||
			*service.Spec.TaskTemplate.RestartPolicy.MaxAttempts == 0)) {
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
	_, failed := TerminalServiceTaskFailure(service.Spec, tasks, service.CreatedAt)
	return failed, nil
}
