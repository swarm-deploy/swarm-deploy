package swarm

import (
	"errors"
	"strings"

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
	if t.CurrentState == TaskStatePending && code != faults.CodeNoSuitableNode {
		return nil
	}
	if (code == "" || code == faults.CodeImagePullDenied) &&
		t.CurrentState == TaskStateRejected && t.Spec.value.ContainerSpec != nil &&
		strings.TrimSpace(t.Spec.value.ContainerSpec.Image) != "" {
		// Registry bodies are arbitrary text. Recognize access refusals there
		// only when the whole message is a standard refusal.
		if message, found := strings.CutPrefix(t.Error, registryErrorPrefix); found {
			return registryPullFault(t.Error, strings.TrimSpace(message))
		}
	}
	if code == "" {
		return nil
	}
	return &dockerFaultError{cause: errors.New(t.Error), code: code, summary: summary}
}

const registryErrorPrefix = "error from registry:"

type registryPullError struct {
	dockerFaultError
	temporary bool
}

func (e *registryPullError) Temporary() bool { return e.temporary }

func registryPullFault(diagnostic, message string) faults.Coded {
	if message == "" {
		return nil
	}
	base := registryResponseMessage(message)
	switch strings.ToLower(base) {
	case "unauthorized", "denied", "authentication required", "requested access to the resource is denied",
		"unauthorized: authentication required", "denied: requested access to the resource is denied",
		"no basic auth credentials", "insufficient_scope: authorization failed":
		return &dockerFaultError{cause: errors.New(diagnostic), code: faults.CodeImagePullDenied, summary: base}
	case "unsupported", "the operation is unsupported.":
		return newRegistryPullError(diagnostic, false)
	default:
		return newRegistryPullError(diagnostic, true)
	}
}

func registryResponseMessage(message string) string {
	first, rest, _ := strings.Cut(message, "\n")
	first = strings.TrimSpace(first)
	// Moby can join multiple registry errors. Treat them as a single standard
	// response only when each line repeats the same diagnostic.
	if rest != "" {
		for line := range strings.SplitSeq(rest, "\n") {
			if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), registryErrorPrefix)) != first {
				return ""
			}
		}
	}
	// Docker can also repeat the diagnostic in a colon-separated error chain.
	if head, tail, ok := strings.Cut(first, ": "); ok &&
		head == strings.TrimSpace(strings.TrimPrefix(tail, registryErrorPrefix)) {
		first = head
	}
	base, _, _ := strings.Cut(first, " - ")
	return base
}

func newRegistryPullError(diagnostic string, temporary bool) *registryPullError {
	return &registryPullError{
		dockerFaultError: dockerFaultError{
			cause: errors.New(diagnostic), code: faults.CodeImagePullFailed, summary: "Docker could not pull the image.",
		},
		temporary: temporary,
	}
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
