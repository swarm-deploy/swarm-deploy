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

func (w *ServiceWaiter) withPendingTaskFaults(
	ctx context.Context, err error, progress map[string]ServiceProgress, includeRegistryPull bool,
) error {
	causes := []error{err}
	var nodes []swarm.Node
	nodesLoaded := false
	for _, name := range sortedProgressNames(progress) {
		service := progress[name]
		taskFaults := service.pendingFaults(includeRegistryPull)
		if service.State != ServicePending || len(taskFaults) == 0 {
			continue
		}
		constraints := placementDiagnosticConstraints(service.placementConstraints)
		for _, fault := range taskFaults {
			if fault.FaultCode() == faults.CodeNoSuitableNode && len(constraints) != 0 && ctx.Err() == nil {
				if !nodesLoaded {
					nodes = w.listPlacementDiagnosticNodes(ctx)
					nodesLoaded = true
				}
				if len(nodes) != 0 && !slices.ContainsFunc(nodes, func(node swarm.Node) bool {
					return constraint.NodeMatches(constraints, node.ConstraintNode())
				}) {
					fault = placementConstraintError{Coded: fault, summary: placementConstraintSummary(service.placementConstraints)}
				}
			}
			causes = append(causes, fmt.Errorf("service %s task diagnostic: %w", name, fault))
		}
	}
	if len(causes) == 1 {
		return err
	}
	return errors.Join(causes...)
}

func (p ServiceProgress) pendingFaults(includeRegistryPull bool) []faults.Coded {
	if includeRegistryPull {
		return slices.Concat(p.pendingTaskFaults, p.registryPullFaults)
	}
	return p.pendingTaskFaults
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
	var schedulingFault faults.Coded
	for _, task := range tasks {
		if task.CurrentState != swarm.TaskStatePending || task.DesiredState != swarm.TaskStateRunning ||
			task.CreatedAt.Before(cutoff) || !task.Spec.CompareServiceTemplate(template).Equal() {
			continue
		}
		if fault := task.Fault(); fault != nil && fault.FaultCode() == faults.CodeNoSuitableNode &&
			(latest.ID == "" || task.UpdatedAt.After(latest.UpdatedAt)) {
			latest = task
			schedulingFault = fault
		}
	}
	if schedulingFault == nil {
		return
	}
	p.pendingTaskFaults = append(p.pendingTaskFaults, schedulingFault)
	if template.Placement != nil {
		p.placementConstraints = template.Placement.Constraints
	}
}

func (p *ServiceProgress) pendingFailureFaults(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, cutoff time.Time,
) swarm.ServiceTask {
	failed := services.UnreplacedServiceTaskFailures(spec, tasks, cutoff)
	var latest swarm.ServiceTask
	if len(failed) != 0 {
		latest = failed[0]
	}
	reportedDenials := make(map[string]struct{})
	if p.Running < p.Desired {
		for i, task := range failed {
			fault := task.Fault()
			if fault == nil {
				continue
			}
			if i == 0 {
				latest.Error = "" // The typed branch supplies the full technical text.
			}
			if fault.FaultCode() == faults.CodeImagePullFailed {
				continue
			}
			p.pendingTaskFaults = append(p.pendingTaskFaults, fault)
			if fault.FaultCode() == faults.CodeImagePullDenied {
				reportedDenials[task.ID] = struct{}{}
			}
		}
	}
	// Swarm's global desired count omits replacements waiting to start.
	if p.Running < p.Desired || spec.Mode.Global != nil {
		p.pendingRegistryPullFaults(spec, tasks, cutoff, reportedDenials)
	}
	return latest
}

func (p *ServiceProgress) pendingRegistryPullFaults(
	spec dockerswarm.ServiceSpec, tasks []swarm.ServiceTask, cutoff time.Time, reportedDenials map[string]struct{},
) {
	for _, task := range services.LatestUnrecoveredTaskAttempts(spec, tasks, cutoff) {
		if _, reported := reportedDenials[task.ID]; reported || task.CurrentState != swarm.TaskStateRejected {
			continue
		}
		if fault := task.Fault(); isImagePullFault(fault) {
			p.registryPullFaults = append(p.registryPullFaults, fault)
		}
	}
}

func isImagePullFault(fault faults.Coded) bool {
	return fault != nil &&
		(fault.FaultCode() == faults.CodeImagePullFailed || fault.FaultCode() == faults.CodeImagePullDenied)
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

func interruptionDiagnostic(
	stackName string, progress map[string]ServiceProgress, cause, lastObservationError error,
) error {
	pending := make([]string, 0)
	for _, name := range sortedProgressNames(progress) {
		service := progress[name]
		if service.State != ServicePending {
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
	return fmt.Errorf("wait for services in stack %s: %w: %s", stackName, cause, diagnostic)
}

func boundedDiagnostic(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxServiceDiagnosticLength {
		return message
	}
	return message[:maxServiceDiagnosticLength] + "..."
}
