// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/moby/swarmkit/v2/manager/constraint"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
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

func (w *ServiceWaiter) withPlacementSchedulingFault(
	ctx context.Context, err error, progress map[string]ServiceProgress,
) error {
	var fallback faults.Coded
	var nodes []swarm.Node
	for _, name := range sortedProgressNames(progress) {
		service := progress[name]
		fault := service.taskFault
		if service.State == ServiceReady || fault == nil || fault.FaultCode() != faults.CodeNoSuitableNode {
			continue
		}
		if fallback == nil {
			fallback = fault
		}
		if ctx.Err() != nil {
			break
		}
		constraints := placementDiagnosticConstraints(service.placementConstraints)
		if len(constraints) == 0 {
			continue
		}
		if nodes == nil {
			nodes = w.listPlacementDiagnosticNodes(ctx)
			if len(nodes) == 0 {
				break
			}
		}
		if !slices.ContainsFunc(nodes, func(node swarm.Node) bool {
			return constraint.NodeMatches(constraints, node.ConstraintNode())
		}) {
			fallback = placementConstraintError{Coded: fault, summary: placementConstraintSummary(service.placementConstraints)}
			break
		}
	}
	if fallback == nil {
		return err
	}
	return fmt.Errorf("%w; task diagnostic: %w", err, fallback)
}

func placementDiagnosticConstraints(raw []string) []constraint.Constraint {
	constraints, err := constraint.Parse(raw)
	if err != nil {
		return nil
	}
	for _, expression := range raw {
		key := expression[:strings.IndexAny(expression, "=!")]
		// Docker exposes the desired role, which may differ from the scheduler's active role.
		if strings.EqualFold(strings.TrimSpace(key), "node.role") {
			return nil
		}
	}
	return constraints
}

func (w *ServiceWaiter) listPlacementDiagnosticNodes(ctx context.Context) []swarm.Node {
	lookupCtx, cancel := context.WithTimeout(ctx, placementDiagnosticTimeout)
	defer cancel()

	nodes, err := w.nodes.List(lookupCtx)
	if err != nil {
		return nil
	}
	return nodes
}

func placementConstraintSummary(raw []string) string {
	label := "constraint"
	if len(raw) > 1 {
		label = "constraints"
	}
	return boundedDiagnostic(fmt.Sprintf("No nodes match placement %s: %s", label, strings.Join(raw, ", ")))
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
	if p.taskFault != nil && template.Placement != nil {
		p.placementConstraints = template.Placement.Constraints
	}
}

func (p *ServiceProgress) pendingRegistryPullFault(
	service swarm.StackService, tasks []swarm.ServiceTask, cutoff time.Time,
) {
	if len(tasks) == 0 {
		return
	}
	if cutoff.IsZero() {
		// Existing-task verification has no cutoff of its own. Use the observed
		// update boundary or, in its absence, the latest accepted service version.
		cutoff = service.UpdatedAt
		if service.UpdateStatus != nil && !service.UpdateStatus.StartedAt.IsZero() {
			cutoff = service.UpdateStatus.StartedAt
		}
		if cutoff.IsZero() {
			cutoff = service.CreatedAt
		}
	}
	if cutoff.IsZero() {
		return
	}
	var latest swarm.ServiceTask
	for _, task := range services.LatestUnrecoveredTaskAttempts(service.ServiceSpec, tasks, cutoff) {
		if task.CurrentState != swarm.TaskStateRejected {
			continue
		}
		fault := task.Fault()
		if isImagePullFault(fault) && (latest.ID == "" || task.UpdatedAt.After(latest.UpdatedAt) ||
			(task.UpdatedAt.Equal(latest.UpdatedAt) && task.ID < latest.ID)) {
			latest, p.taskFault = task, fault
		}
	}
	if p.taskFault != nil {
		latest.Error = ""
		p.taskContext = formatTaskDiagnostic(latest)
	}
}

func isImagePullFault(fault faults.Coded) bool {
	return fault != nil &&
		(fault.FaultCode() == faults.CodeImagePullFailed || fault.FaultCode() == faults.CodeImagePullDenied)
}

type registryPullTimeoutError struct {
	error
	temporary bool
}

func (e registryPullTimeoutError) Unwrap() error   { return e.error }
func (e registryPullTimeoutError) Temporary() bool { return e.temporary }

func withPendingRegistryPullFault(err error, progress map[string]ServiceProgress) error {
	for _, name := range sortedProgressNames(progress) {
		service := progress[name]
		if service.State != ServiceReady && isImagePullFault(service.taskFault) {
			// The deadline and an earlier API error may themselves be temporary.
			// Keep the classification of the selected task diagnostic.
			classified, ok := errors.AsType[faults.Error](service.taskFault)
			return registryPullTimeoutError{
				error:     fmt.Errorf("service %s%s: %w; %w", name, service.taskContext, service.taskFault, err),
				temporary: ok && classified.Temporary(),
			}
		}
	}
	return err
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
	task := latestFailedTask(tasks)
	// Registry diagnostics require rollout evidence before they are reported.
	if isImagePullFault(task.Fault()) {
		return ""
	}
	return formatTaskDiagnostic(task)
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
		diagnostic += ": " + boundedDiagnostic(faults.SafeDiagnostic(errors.New(task.Error)))
	}
	return diagnostic
}

func (w *ServiceWaiter) waitInterruptedError(
	ctx context.Context,
	stackName string,
	state *waitState,
	cause error,
) error {
	progress := state.last
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
	timedOut := cause == context.DeadlineExceeded
	if state.lastObservationError != nil {
		cause = fmt.Errorf("%w; last Docker API error: %w", cause, state.lastObservationError)
	}
	err := fmt.Errorf("wait for services in stack %s: %w: %s", stackName, cause, diagnostic)
	err = w.withPlacementSchedulingFault(ctx, err, progress)
	if timedOut && state.terminalFailure == nil {
		err = withPendingRegistryPullFault(err, progress)
	}
	return errors.Join(state.terminalFailure, err)
}

func boundedDiagnostic(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxServiceDiagnosticLength {
		return message
	}
	return message[:maxServiceDiagnosticLength] + "..."
}
