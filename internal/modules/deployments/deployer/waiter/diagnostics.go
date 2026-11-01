// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func terminalTaskDiagnostic(tasks []swarm.ServiceTask, updateMessage string) string {
	// Swarm can pause after an early successful exit too. Prefer the task Docker
	// names in its update message over a newer, unrelated historical failure.
	for _, task := range tasks {
		if task.ID != "" && strings.Contains(updateMessage, task.ID) {
			return formatTaskDiagnostic(task)
		}
	}
	return taskFailureDiagnostic(tasks)
}

func taskFailureDiagnostic(tasks []swarm.ServiceTask) string {
	failed := make([]swarm.ServiceTask, 0)
	for _, task := range tasks {
		if task.CurrentState == swarm.TaskStateFailed ||
			task.CurrentState == swarm.TaskStateRejected ||
			task.CurrentState == swarm.TaskStateOrphaned {
			failed = append(failed, task)
		}
	}
	if len(failed) == 0 {
		return ""
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].UpdatedAt.After(failed[j].UpdatedAt) })
	return formatTaskDiagnostic(failed[0])
}

func formatTaskDiagnostic(task swarm.ServiceTask) string {
	diagnostic := fmt.Sprintf("; latest task failure: id=%s state=%s", task.ID, task.CurrentState)
	if task.ExitCode != nil {
		diagnostic += fmt.Sprintf(" exit_code=%d", *task.ExitCode)
	}
	if task.Error != "" {
		diagnostic += ": " + boundedDiagnostic(task.Error)
	}
	return diagnostic
}

func waitInterruptedError(
	stackName string,
	progress map[string]ServiceProgress,
	cause error,
	lastObservationError error,
) error {
	pending := make([]string, 0)
	for _, name := range sortedProgressNames(progress) {
		service := progress[name]
		if service.State == ServiceReady {
			continue
		}
		pending = append(pending, fmt.Sprintf(
			"%s (%d/%d running: %s)",
			name,
			service.Running,
			service.Desired,
			service.Message,
		))
	}
	diagnostic := strings.Join(pending, "; ")
	if lastObservationError != nil {
		if diagnostic != "" {
			diagnostic += "; "
		}
		diagnostic += fmt.Sprintf("last Docker API error: %v", lastObservationError)
	}
	return fmt.Errorf("wait for services in stack %s: %w: %s", stackName, cause, diagnostic)
}

func boundedDiagnostic(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxServiceDiagnosticLength {
		return message
	}
	return message[:maxServiceDiagnosticLength] + "..."
}
