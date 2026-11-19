package waiter

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func jobProgress(
	progress ServiceProgress, baseline, service swarm.StackService, tasks []swarm.ServiceTask,
) ServiceProgress {
	progress.Message = "waiting for job scheduler"
	if service.JobStatus == nil {
		return progress
	}
	if baseline.ID == service.ID && baseline.JobStatus != nil &&
		!reflect.DeepEqual(baseline.ServiceSpec, service.ServiceSpec) &&
		baseline.JobStatus.JobIteration == service.JobStatus.JobIteration {
		return progress
	}
	desired := jobDesiredTasks(service, tasks)
	progress.Desired = desired
	if task, failed := services.JobTaskFailure(service.ServiceSpec, service.JobStatus, tasks); failed {
		progress.failTask(task, "job task exited without further retries")
		progress.Message = boundedDiagnostic(faults.SafeDiagnostic(errors.New(progress.Message)))
		return progress
	}
	if (desired > 0 || service.Mode != globalJobServiceMode) && service.CompletedTasks >= desired {
		progress.State = ServiceReady
		progress.Message = "service job completed"
		return progress
	}
	attempts := services.LatestUnrecoveredJobAttempts(service.ServiceSpec, service.JobStatus, tasks)
	progress.pendingRegistryPullFaults(attempts, nil)
	if desired > 0 {
		progress.Message = fmt.Sprintf("waiting for job completion: %d/%d completed", service.CompletedTasks, desired)
	}
	return progress
}

func jobDesiredTasks(service swarm.StackService, tasks []swarm.ServiceTask) uint64 {
	if job := service.ServiceSpec.Mode.ReplicatedJob; job != nil {
		if job.TotalCompletions != nil {
			return *job.TotalCompletions
		}
		if job.MaxConcurrent != nil {
			return *job.MaxConcurrent
		}
		return 1
	}
	// Swarm's global job summary omits replacements held at READY during restart delay.
	var outstanding uint64
	for _, task := range tasks {
		if task.JobIteration != nil && *task.JobIteration == service.JobStatus.JobIteration &&
			(task.DesiredState == swarm.TaskStateReady || task.DesiredState == swarm.TaskStateComplete) &&
			task.CurrentState != swarm.TaskStateComplete {
			outstanding++
		}
	}
	return service.CompletedTasks + max(service.DesiredTasks, outstanding)
}
