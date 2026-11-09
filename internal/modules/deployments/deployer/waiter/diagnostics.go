// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	swarmkitapi "github.com/moby/swarmkit/v2/api"
	"github.com/moby/swarmkit/v2/manager/constraint"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const placementDiagnosticTimeout = 250 * time.Millisecond

type placementConstraintError struct {
	faults.Coded
	summary string
}

func (e placementConstraintError) FaultSummary() string { return e.summary }
func (e placementConstraintError) Unwrap() error        { return e.Coded }

func (w *ServiceWaiter) enrichPlacementSchedulingFaults(
	ctx context.Context,
	observed []ServiceProgress,
	current map[string]swarm.StackService,
	nodes map[string]*swarmkitapi.Node,
) {
	for i := range observed {
		progress := &observed[i]
		fault := progress.taskFault
		if progress.State != ServicePending || fault == nil || fault.FaultCode() != faults.CodeNoSuitableNode {
			continue
		}
		placement := current[progress.Name].ServiceSpec.TaskTemplate.Placement
		if placement == nil || len(placement.Constraints) == 0 {
			continue
		}
		constraints, err := constraint.Parse(placement.Constraints)
		if err != nil {
			continue
		}
		if nodes == nil {
			nodes = w.listPlacementDiagnosticNodes(ctx)
		}
		if len(nodes) == 0 {
			return
		}
		enrichPlacementSchedulingFault(progress, placement.Constraints, constraints, nodes)
	}
}

func (w *ServiceWaiter) listPlacementDiagnosticNodes(ctx context.Context) map[string]*swarmkitapi.Node {
	ctx, cancel := context.WithTimeout(ctx, placementDiagnosticTimeout)
	defer cancel()

	nodes, err := w.nodes.List(ctx)
	if err != nil {
		return nil
	}
	return placementNodesByID(nodes)
}

func enrichPlacementSchedulingFault(
	progress *ServiceProgress, raw []string, constraints []constraint.Constraint, nodes map[string]*swarmkitapi.Node,
) {
	for _, node := range nodes {
		if nodeMatchesConstraints(constraints, node) {
			return
		}
	}
	label := "constraint"
	if len(constraints) > 1 {
		label = "constraints"
	}
	summary := fmt.Sprintf("No nodes match placement %s: %s", label, strings.Join(raw, ", "))
	progress.taskFault = placementConstraintError{Coded: progress.taskFault, summary: boundedDiagnostic(summary)}
}

func (p *ServiceProgress) pendingSchedulingFault(
	tasks []swarm.ServiceTask, template dockerswarm.TaskSpec, cutoff time.Time,
) {
	var latest swarm.ServiceTask
	for _, task := range tasks {
		if task.CurrentState != swarm.TaskStatePending || task.DesiredState != swarm.TaskStateRunning ||
			task.CreatedAt.Before(cutoff) || !task.Spec.CompareServiceTemplate(template).Equal() {
			continue
		}
		if fault := task.Fault(); fault != nil && fault.FaultCode() == faults.CodeNoSuitableNode &&
			(latest.ID == "" || task.UpdatedAt.After(latest.UpdatedAt)) {
			latest = task
			p.taskFault = fault
		}
	}
}

func terminalTaskCutoff(baseline, service swarm.StackService) time.Time {
	cutoff := service.UpdateStatus.StartedAt
	if cutoff.IsZero() {
		cutoff = service.UpdatedAt
		if baseline.ID == service.ID && !baseline.UpdatedAt.IsZero() {
			cutoff = baseline.UpdatedAt
		}
		if cutoff.IsZero() {
			cutoff = service.CreatedAt
		}
	}
	if strings.HasPrefix(string(service.UpdateStatus.State), "rollback_") &&
		baseline.ID == service.ID && serviceChanged(baseline, service) && !baseline.UpdatedAt.IsZero() {
		cutoff = baseline.UpdatedAt
	}
	return cutoff
}

func terminalFailureTask(tasks []swarm.ServiceTask, updateMessage string, cutoff time.Time) swarm.ServiceTask {
	// Match a complete ID in Docker's message, including an early successful exit.
	words := strings.FieldsFunc(updateMessage, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
	})
	for _, task := range tasks {
		if task.ID != "" && slices.Contains(words, task.ID) {
			return task
		}
	}
	if cutoff.IsZero() {
		return swarm.ServiceTask{} // No generation evidence for a safe fallback.
	}
	// Historical failures cannot explain this rollout. Rollback callers use the
	// pre-apply boundary because failed new tasks can predate rollback StartedAt.
	current := slices.DeleteFunc(slices.Clone(tasks), func(task swarm.ServiceTask) bool {
		return task.CreatedAt.Before(cutoff)
	})
	return latestFailedTask(current)
}

func taskFailureDiagnostic(tasks []swarm.ServiceTask) string {
	return formatTaskDiagnostic(latestFailedTask(tasks))
}

func latestFailedTask(tasks []swarm.ServiceTask) swarm.ServiceTask {
	failed := make([]swarm.ServiceTask, 0)
	for _, task := range tasks {
		if task.CurrentState == swarm.TaskStateFailed ||
			task.CurrentState == swarm.TaskStateRejected ||
			task.CurrentState == swarm.TaskStateOrphaned {
			failed = append(failed, task)
		}
	}
	if len(failed) == 0 {
		return swarm.ServiceTask{}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].UpdatedAt.After(failed[j].UpdatedAt) })
	return failed[0]
}

func formatTaskDiagnostic(task swarm.ServiceTask) string {
	if task.ID == "" {
		return ""
	}
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
		cause = fmt.Errorf("%w; last Docker API error: %w", cause, lastObservationError)
	}
	err := fmt.Errorf("wait for services in stack %s: %w: %s", stackName, cause, diagnostic)
	for _, name := range sortedProgressNames(progress) {
		service := progress[name]
		if service.State != ServiceReady && service.taskFault != nil &&
			service.taskFault.FaultCode() == faults.CodeNoSuitableNode {
			return fmt.Errorf("%w; task diagnostic: %w", err, service.taskFault)
		}
	}
	return err
}

func boundedDiagnostic(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxServiceDiagnosticLength {
		return message
	}
	return message[:maxServiceDiagnosticLength] + "..."
}
