package swarm

import (
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// Fault returns an explicitly diagnosed task failure, including pending scheduling failures.
// It does not decide whether the failure is terminal or whether a retry can succeed.
func (t ServiceTask) Fault() faults.Coded {
	if t.ImageNotFound() {
		return &dockerFaultError{cause: errors.New(t.Error), code: faults.CodeImageNotFound, summary: t.Error}
	}
	if t.CurrentState != TaskStateFailed && t.CurrentState != TaskStateRejected &&
		t.CurrentState != TaskStateOrphaned && t.CurrentState != TaskStatePending {
		return nil
	}
	code, summary := dockerFault(t.Error)
	if code == "" || (t.CurrentState == TaskStatePending && code != faults.CodeNoSuitableNode) {
		return nil
	}
	return &dockerFaultError{cause: errors.New(t.Error), code: code, summary: summary}
}

// TaskState represents the state of a task.
type TaskState string

const (
	// TaskStateNew indicates that the task was newly created.
	TaskStateNew TaskState = "new"
	// TaskStateAllocated indicates that resources were allocated to the task.
	TaskStateAllocated TaskState = "allocated"
	// TaskStatePending indicates that the task is waiting for assignment.
	TaskStatePending TaskState = "pending"
	// TaskStateAssigned indicates that the task was assigned to a node.
	TaskStateAssigned TaskState = "assigned"
	// TaskStateAccepted indicates that the assigned node accepted the task.
	TaskStateAccepted TaskState = "accepted"
	// TaskStatePreparing indicates that the node is preparing the task.
	TaskStatePreparing TaskState = "preparing"
	// TaskStateReady indicates that the task is ready to start.
	TaskStateReady TaskState = "ready"
	// TaskStateStarting indicates that the task is starting.
	TaskStateStarting TaskState = "starting"
	// TaskStateRunning indicates that the task is running.
	TaskStateRunning TaskState = "running"
	// TaskStateComplete indicates that the task completed successfully.
	TaskStateComplete TaskState = "complete"
	// TaskStateShutdown indicates that the task was shut down.
	TaskStateShutdown TaskState = "shutdown"
	// TaskStateFailed indicates that the task failed.
	TaskStateFailed TaskState = "failed"
	// TaskStateRejected indicates that the task was rejected.
	TaskStateRejected TaskState = "rejected"
	// TaskStateRemove indicates that the task is being removed.
	TaskStateRemove TaskState = "remove"
	// TaskStateOrphaned indicates that the task is orphaned.
	TaskStateOrphaned TaskState = "orphaned"
)
