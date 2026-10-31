package waiter

import (
	"fmt"
	"reflect"

	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func jobProgress(progress ServiceProgress, baseline, service swarm.StackService) ServiceProgress {
	progress.Message = "waiting for job scheduler"
	if service.JobStatus == nil {
		return progress
	}
	if baseline.ID == service.ID && baseline.JobStatus != nil &&
		!reflect.DeepEqual(baseline.ServiceSpec, service.ServiceSpec) &&
		baseline.JobStatus.JobIteration == service.JobStatus.JobIteration {
		return progress
	}
	// For global jobs Swarm reports outstanding desired tasks separately from
	// completed tasks. Sum them so partial completion cannot look converged.
	desired := service.DesiredTasks + service.CompletedTasks
	if job := service.ServiceSpec.Mode.ReplicatedJob; job != nil {
		desired = 1
		if job.MaxConcurrent != nil {
			desired = *job.MaxConcurrent
		}
		if job.TotalCompletions != nil {
			desired = *job.TotalCompletions
		}
	}
	progress.Desired = desired
	if service.Mode == "global-job" && desired == 0 {
		return progress
	}
	if service.CompletedTasks >= desired {
		progress.State = ServiceReady
		progress.Message = "service job completed"
	} else {
		progress.Message = fmt.Sprintf("waiting for job completion: %d/%d completed", service.CompletedTasks, desired)
	}
	return progress
}
