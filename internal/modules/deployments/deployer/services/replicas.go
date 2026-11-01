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

func (a *Applier) checkUpdateSafety(
	ctx context.Context, name string, current dockerswarm.Service, desired dockerswarm.ServiceSpec,
) error {
	if !taskRecreatingUpdate(current.Spec, desired) {
		return nil
	}
	// A completed task with replicas still set to one can run again on a template update.
	if current.Spec.Mode.Replicated == nil || current.Spec.Mode.Replicated.Replicas == nil ||
		*current.Spec.Mode.Replicated.Replicas != 0 {
		return fmt.Errorf("%w: service %s requires zero replicas before applying task-recreating changes",
			ErrUpdateBlocked, name)
	}
	tasks, err := a.manager.ListTasks(ctx, current.ID)
	if err != nil {
		return fmt.Errorf("check service %s idle tasks: %w", name, err)
	}
	for _, task := range tasks {
		switch task.CurrentState {
		case swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed,
			swarm.TaskStateRejected, swarm.TaskStateRemove, swarm.TaskStateOrphaned:
			continue
		case swarm.TaskStateNew, swarm.TaskStateAllocated, swarm.TaskStatePending, swarm.TaskStateAssigned,
			swarm.TaskStateAccepted, swarm.TaskStatePreparing, swarm.TaskStateReady, swarm.TaskStateStarting,
			swarm.TaskStateRunning:
			return fmt.Errorf("%w: service %s still has active tasks", ErrUpdateBlocked, name)
		default:
			return fmt.Errorf("%w: service %s still has active tasks", ErrUpdateBlocked, name)
		}
	}
	return nil
}

func taskRecreatingUpdate(current, desired dockerswarm.ServiceSpec) bool {
	// Only service metadata and rollout policy changes are known to be safe while busy.
	// Conservatively require idle state for all other changes, including mode and endpoint changes.
	current.Annotations = desired.Annotations
	current.UpdateConfig = desired.UpdateConfig
	current.RollbackConfig = desired.RollbackConfig
	return current.TaskTemplate.ForceUpdate != desired.TaskTemplate.ForceUpdate || !SpecsEqual(current, desired)
}
