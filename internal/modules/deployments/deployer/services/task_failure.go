package services

import (
	"context"
	"fmt"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// NonRestartingTaskFailure finds an exited current-generation replica that cannot
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
		if task.CurrentState == swarm.TaskStateFailed && task.ExitCode != nil && *task.ExitCode != 0 &&
			(failed.ID == "" || task.UpdatedAt.After(failed.UpdatedAt)) {
			failed = task
		}
	}
	return failed, failed.ID != ""
}

func (a *Applier) retryRequired(ctx context.Context, service dockerswarm.Service, enabled bool) (bool, error) {
	if !enabled {
		return false, nil
	}
	if failedUpdate(service) {
		return true, nil
	}
	if service.UpdateStatus != nil || service.Spec.TaskTemplate.RestartPolicy == nil ||
		service.Spec.TaskTemplate.RestartPolicy.Condition != dockerswarm.RestartPolicyConditionNone {
		return false, nil
	}
	tasks, err := a.manager.ListTasks(ctx, service.ID)
	if err != nil {
		return false, fmt.Errorf("inspect service %s retry tasks: %w", service.Spec.Name, err)
	}
	_, failed := NonRestartingTaskFailure(service.Spec, tasks, service.UpdatedAt)
	return failed, nil
}
