package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ErrUpdateBlocked identifies a specification change that could interrupt or start an external job.
var ErrUpdateBlocked = errors.New("externally managed service update blocked")

// ErrUpdateDeferred marks an apply whose only failures are external updates waiting for
// requested executions to settle. Such an apply can succeed later without operator action.
var ErrUpdateDeferred = errors.New("externally managed service update deferred until active tasks finish")

// errUpdateBusy keeps the ErrUpdateBlocked message while marking a transient block.
var errUpdateBusy = fmt.Errorf("%w", ErrUpdateBlocked)

// errSafetyInspectionChanged restarts preparation with a newly inspected service.
var errSafetyInspectionChanged = errors.New("service changed during external execution inspection")

const (
	templateMismatchRechecks       = 3
	templateMismatchInitialBackoff = 100 * time.Millisecond
)

type executionSettlementReason string

const (
	executionSettledReason          executionSettlementReason = "settled"
	executionMissingSlotReason      executionSettlementReason = "missing slot"
	executionActiveTaskReason       executionSettlementReason = "active task"
	executionRemovedTaskReason      executionSettlementReason = "removed task"
	executionTemplateMismatchReason executionSettlementReason = "template mismatch"
)

type executionSettlement struct {
	reason               executionSettlementReason
	slot                 int
	mismatchPaths        []string
	olderForceUpdateOnly bool
}

// blockedApplyError joins per-service blocked errors and marks them deferred
// only when every block is expected to clear after requested executions settle.
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
//
// No active tasks alone does not prove that a requested execution has happened: the
// scheduler may already have raised replicas or ForceUpdate while Swarm has not created
// the task yet, and this inspection may have read that version. Parking therefore also
// requires executionSettled for the inspected version. Any later scheduler write changes
// the service version, so the optimistic update conflicts and is re-evaluated.
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
	settlement := executionSettlement{reason: executionSettledReason}
	if park {
		settlement = executionSettled(current.Spec.TaskTemplate, replicas, tasks)
	} else {
		for _, task := range tasks {
			if taskActive(task.CurrentState) {
				settlement = executionSettlement{reason: executionActiveTaskReason, slot: task.Slot}
				break
			}
		}
	}
	if settlement.reason == executionTemplateMismatchReason {
		settlement, err = a.recheckTemplateMismatch(ctx, name, current, replicas, settlement)
		if err != nil {
			return err
		}
	}
	switch settlement.reason {
	case executionSettledReason:
	case executionMissingSlotReason:
		return fmt.Errorf(
			"%w: service %s execution is unsettled: missing task for slot %d",
			errUpdateBusy, name, settlement.slot,
		)
	case executionActiveTaskReason:
		return fmt.Errorf(
			"%w: service %s execution is unsettled: active task in slot %d",
			errUpdateBusy, name, settlement.slot,
		)
	case executionRemovedTaskReason:
		return fmt.Errorf(
			"%w: service %s execution is unsettled: newest task for slot %d is being removed",
			errUpdateBusy, name, settlement.slot,
		)
	case executionTemplateMismatchReason:
		if settlement.olderForceUpdateOnly {
			return fmt.Errorf(
				"%w: service %s execution is unsettled: newest task for slot %d has an older ForceUpdate generation",
				errUpdateBusy, name, settlement.slot,
			)
		}
		return fmt.Errorf(
			"%w: service %s task template mismatch persisted across %d observations over %s at fields %s; "+
				"retry after Docker finishes scheduling or verify Engine/API compatibility",
			ErrUpdateBlocked,
			name,
			templateMismatchRechecks+1,
			templateMismatchTotalBackoff(),
			mismatchPathDiagnostic(settlement.mismatchPaths),
		)
	default:
		return fmt.Errorf("%w: service %s execution settlement is unknown", ErrUpdateBlocked, name)
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

// executionSettled mirrors the replicated orchestrator's slot logic for restart_policy none:
// Swarm has no pending task for the inspected service version only when every desired slot's
// newest task was created from the current template (including ForceUpdate), is terminal and
// is not being removed. A missing slot, a removed slot task, or a newest task from an older
// template means Swarm may still create the execution requested by the external scheduler.
func executionSettled(template dockerswarm.TaskSpec, replicas uint64, tasks []swarm.ServiceTask) executionSettlement {
	for _, task := range tasks {
		if taskActive(task.CurrentState) {
			return executionSettlement{reason: executionActiveTaskReason, slot: task.Slot}
		}
	}
	newest := make(map[int]swarm.ServiceTask, replicas)
	for _, task := range tasks {
		if task.Slot < 1 || uint64(task.Slot) > replicas {
			continue
		}
		previous, exists := newest[task.Slot]
		if !exists || task.CreatedAt.After(previous.CreatedAt) {
			newest[task.Slot] = task
		}
	}
	settlement := executionSettlement{reason: executionSettledReason}
	for slot := 1; uint64(slot) <= replicas; slot++ {
		task, exists := newest[slot]
		if !exists {
			return executionSettlement{reason: executionMissingSlotReason, slot: slot}
		}
		if task.DesiredState == swarm.TaskStateRemove {
			return executionSettlement{reason: executionRemovedTaskReason, slot: slot}
		}
		comparison := task.Spec.CompareServiceTemplate(template)
		if comparison.Equal() {
			continue
		}
		mismatch := executionSettlement{
			reason: executionTemplateMismatchReason, slot: slot, mismatchPaths: comparison.MismatchPaths(),
			olderForceUpdateOnly: comparison.OlderForceUpdateOnly(),
		}
		if !mismatch.olderForceUpdateOnly {
			return mismatch
		}
		// A stale generation in this slot must not mask a different mismatch in a later slot.
		settlement = mismatch
	}
	return settlement
}

func (a *Applier) recheckTemplateMismatch(
	ctx context.Context, name string, current dockerswarm.Service, replicas uint64, settlement executionSettlement,
) (executionSettlement, error) {
	for attempt := 0; attempt <= templateMismatchRechecks; attempt++ {
		latest, err := a.manager.Inspect(ctx, current.ID)
		if err != nil {
			return settlement, fmt.Errorf("reinspect service %s during task template comparison: %w", name, err)
		}
		if latest.ID != current.ID || latest.Version.Index != current.Version.Index {
			return settlement, errSafetyInspectionChanged
		}
		if settlement.reason != executionTemplateMismatchReason {
			return settlement, nil
		}
		if attempt == templateMismatchRechecks {
			return settlement, nil
		}
		backoff := templateMismatchInitialBackoff << attempt
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return settlement, ctx.Err()
		case <-timer.C:
		}
		tasks, err := a.manager.ListTasks(ctx, current.ID)
		if err != nil {
			return settlement, fmt.Errorf("recheck service %s idle tasks: %w", name, err)
		}
		settlement = executionSettled(current.Spec.TaskTemplate, replicas, tasks)
	}
	return settlement, nil
}

func templateMismatchTotalBackoff() time.Duration {
	total := time.Duration(0)
	for attempt := 0; attempt < templateMismatchRechecks; attempt++ {
		total += templateMismatchInitialBackoff << attempt
	}
	return total
}

func mismatchPathDiagnostic(paths []string) string {
	const maximumPaths = 6
	if len(paths) == 0 {
		return "TaskTemplate"
	}
	if len(paths) <= maximumPaths {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s (+%d more fields)", strings.Join(paths[:maximumPaths], ", "), len(paths)-maximumPaths)
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
