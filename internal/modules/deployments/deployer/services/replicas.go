package services

import (
	"context"
	"errors"
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ErrUpdateBlocked identifies a specification change that could interrupt or start an external job.
var ErrUpdateBlocked = errors.New("externally managed service update blocked")

// ErrUpdateDeferred marks an apply whose only failures are external updates waiting for
// active tasks to finish. Such an apply can succeed later without operator action.
var ErrUpdateDeferred = errors.New("externally managed service update deferred until active tasks finish")

// errUpdateBusy keeps the ErrUpdateBlocked message while marking a transient block.
var errUpdateBusy = fmt.Errorf("%w", ErrUpdateBlocked)

// blockedApplyError joins per-service blocked errors and marks them deferred
// only when every block is expected to clear after active tasks finish.
func blockedApplyError(blocked []error) error {
	if len(blocked) == 0 {
		return nil
	}
	joined := errors.Join(blocked...)
	for _, err := range blocked {
		if !errors.Is(err, errUpdateBusy) {
			return joined
		}
	}
	return fmt.Errorf("%w: %w", ErrUpdateDeferred, joined)
}

// ReplicasManaged resolves the existing Compose boolean policy, defaulting ownership to swarm-deploy.
func ReplicasManaged(spec dockerswarm.ServiceSpec) (bool, error) {
	service := compose.Service{Deploy: compose.ServiceDeploy{Labels: *compose.NewLabels(spec.Labels)}}
	managed, specified, err := service.ReplicasManagedPolicy()
	if err == nil && specified && !managed && (spec.Mode.Replicated == nil || spec.Mode.Global != nil ||
		spec.Mode.ReplicatedJob != nil || spec.Mode.GlobalJob != nil) {
		err = fmt.Errorf("replicasManaged=false is supported only for replicated services")
	}
	return !specified || managed, err
}

func preserveReplicas(current dockerswarm.ServiceSpec, desired *dockerswarm.ServiceSpec) {
	if current.Mode.Replicated != nil && desired.Mode.Replicated != nil {
		desired.Mode.Replicated.Replicas = current.Mode.Replicated.Replicas
	}
}

// checkUpdateSafety allows task-recreating changes only for idle external services.
// An idle service with restart_policy.condition=none and nonzero replicas is parked:
// the update also sets replicas to zero, so Swarm cannot start an unscheduled execution
// from a completed slot. The external scheduler scales it up again on its next run.
// Optimistic versioning makes this safe: a scheduler run between inspection and
// update changes the service version, so the update conflicts and is re-evaluated.
func (a *Applier) checkUpdateSafety(
	ctx context.Context, name string, current dockerswarm.Service, desired *dockerswarm.ServiceSpec,
) error {
	if !taskRecreatingUpdate(current.Spec, *desired) {
		return nil
	}
	replicas, known := currentReplicas(current.Spec)
	park := known && replicas != 0 && restartDisabled(current.Spec) && desired.Mode.Replicated != nil
	if !known || (replicas != 0 && !park) {
		// A long-running service with external replicas cannot be stopped safely here.
		return fmt.Errorf("%w: service %s requires zero replicas before applying task-recreating changes",
			ErrUpdateBlocked, name)
	}
	tasks, err := a.manager.ListTasks(ctx, current.ID)
	if err != nil {
		return fmt.Errorf("check service %s idle tasks: %w", name, err)
	}
	for _, task := range tasks {
		if taskActive(task.CurrentState) {
			return fmt.Errorf("%w: service %s still has active tasks", errUpdateBusy, name)
		}
	}
	if park {
		zero := uint64(0)
		desired.Mode.Replicated.Replicas = &zero
	}
	return nil
}

func currentReplicas(spec dockerswarm.ServiceSpec) (uint64, bool) {
	if spec.Mode.Replicated == nil || spec.Mode.Replicated.Replicas == nil {
		return 0, false
	}
	return *spec.Mode.Replicated.Replicas, true
}

// restartDisabled identifies job-like services: Swarm never restarts their completed tasks.
func restartDisabled(spec dockerswarm.ServiceSpec) bool {
	policy := spec.TaskTemplate.RestartPolicy
	return policy != nil && policy.Condition == dockerswarm.RestartPolicyConditionNone
}

func taskActive(state swarm.TaskState) bool {
	switch state {
	case swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed,
		swarm.TaskStateRejected, swarm.TaskStateRemove, swarm.TaskStateOrphaned:
		return false
	case swarm.TaskStateNew, swarm.TaskStateAllocated, swarm.TaskStatePending, swarm.TaskStateAssigned,
		swarm.TaskStateAccepted, swarm.TaskStatePreparing, swarm.TaskStateReady, swarm.TaskStateStarting,
		swarm.TaskStateRunning:
		return true
	default:
		return true // Unknown future states are conservatively active.
	}
}

func taskRecreatingUpdate(current, desired dockerswarm.ServiceSpec) bool {
	// Only service metadata and rollout policy changes are known to be safe while busy.
	// Conservatively require idle state for all other changes, including mode and endpoint changes.
	current.Annotations = desired.Annotations
	current.UpdateConfig = desired.UpdateConfig
	current.RollbackConfig = desired.RollbackConfig
	return current.TaskTemplate.ForceUpdate != desired.TaskTemplate.ForceUpdate || !SpecsEqual(current, desired)
}
