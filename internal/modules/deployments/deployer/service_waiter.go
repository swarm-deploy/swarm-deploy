package deployer

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultServiceWaitTimeout  = 10 * time.Minute
	maxServiceDiagnosticLength = 256
)

var serviceWaiterTracer = otel.Tracer(
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/servicewaiter",
)

// ServiceState describes the current convergence state of a service.
type ServiceState string

const (
	// ServicePending means that the service has not converged yet.
	ServicePending ServiceState = "pending"
	// ServiceReady means that the service has converged.
	ServiceReady ServiceState = "ready"
	// ServiceFailed means that the service cannot converge without another update.
	ServiceFailed ServiceState = "failed"
)

// ServiceProgress contains one observable service convergence state.
type ServiceProgress struct {
	// ServiceID is the Docker service identifier when the service is visible.
	ServiceID string
	// Name is the service name without the stack prefix.
	Name string
	// State is the current convergence state.
	State ServiceState
	// Desired is the number of tasks expected to run.
	Desired uint64
	// Running is the number of current desired-state tasks that are running.
	Running uint64
	// Message contains a bounded convergence diagnostic.
	Message string
}

// ServiceProgressCallback receives meaningful service convergence transitions.
type ServiceProgressCallback func(ServiceProgress)

// ServiceWaitRequest defines the services affected by one detached stack apply.
type ServiceWaitRequest struct {
	// StackName is the Docker stack namespace.
	StackName string
	// ServiceNames contains service names without the stack prefix.
	ServiceNames []string
	// Baseline is the stack service snapshot captured immediately before apply.
	Baseline []swarm.StackService
	// OnlyAffected excludes unchanged services from a full-stack wait.
	OnlyAffected bool
	// Progress receives de-duplicated convergence transitions when configured.
	Progress ServiceProgressCallback
}

// ServiceWaiter observes Docker Swarm service convergence after a detached apply.
type ServiceWaiter struct {
	services     swarm.ServiceManager
	pollInterval time.Duration
	timeout      time.Duration
}

// NewServiceWaiter creates a bounded Docker Swarm service convergence waiter.
func NewServiceWaiter(
	services swarm.ServiceManager,
	pollInterval time.Duration,
	timeout time.Duration,
) *ServiceWaiter {
	return &ServiceWaiter{services: services, pollInterval: pollInterval, timeout: timeout}
}

// Snapshot returns the current services belonging to a stack.
func (w *ServiceWaiter) Snapshot(ctx context.Context, stackName string) ([]swarm.StackService, error) {
	services, err := w.services.ListServices(ctx, swarm.ListServicesFilter{Stack: stackName})
	if err != nil {
		return nil, fmt.Errorf("snapshot services for stack %s: %w", stackName, err)
	}

	return services, nil
}

// Wait blocks until the requested affected services converge or the wait is interrupted.
func (w *ServiceWaiter) Wait(ctx context.Context, request ServiceWaitRequest) (waitErr error) {
	startedAt := time.Now()
	waitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	waitCtx, span := serviceWaiterTracer.Start(waitCtx, "serviceWaiter.Wait", trace.WithAttributes(
		tracing.ResourceStackName.String(request.StackName),
	))
	defer span.End()

	state := waitState{last: make(map[string]ServiceProgress)}
	defer func() {
		finishServiceWaitSpan(span, startedAt, state, waitErr)
	}()

	waitErr = w.wait(waitCtx, span, request, &state)
	return waitErr
}

func (w *ServiceWaiter) wait(
	ctx context.Context,
	span trace.Span,
	request ServiceWaitRequest,
	state *waitState,
) error {

	baseline := servicesByName(request.Baseline)
	candidates := uniqueSortedNames(request.ServiceNames)
	targets := initialTargets(candidates, baseline, request.OnlyAffected)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		current, err := w.Snapshot(ctx, request.StackName)
		if err != nil {
			state.failPending(span, request.Progress, err)
			if ctx.Err() != nil {
				return waitInterruptedError(request.StackName, state.last, ctx.Err())
			}
			return err
		}
		currentByName := servicesByName(current)
		if request.OnlyAffected {
			discoverAffectedTargets(targets, candidates, baseline, currentByName)
		}

		if len(targets) == 0 {
			return nil
		}

		allReady, observeErr := w.observeTargets(ctx, span, request.Progress, baseline, currentByName, targets, state)
		if observeErr != nil {
			state.failPending(span, request.Progress, observeErr)
			if ctx.Err() != nil {
				return waitInterruptedError(request.StackName, state.last, ctx.Err())
			}
			return observeErr
		}
		if allReady {
			return nil
		}

		select {
		case <-ctx.Done():
			state.failPending(span, request.Progress, ctx.Err())
			return waitInterruptedError(request.StackName, state.last, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (w *ServiceWaiter) observeTargets(
	ctx context.Context,
	span trace.Span,
	callback ServiceProgressCallback,
	baseline map[string]swarm.StackService,
	current map[string]swarm.StackService,
	targets map[string]struct{},
	state *waitState,
) (bool, error) {
	state.observed = len(targets)
	state.converged = 0
	allReady := true
	for _, name := range sortedTargetNames(targets) {
		progress, err := w.observeService(ctx, name, baseline[name], current[name])
		state.emit(span, callback, progress)
		if err != nil {
			return false, err
		}
		if progress.State == ServiceFailed {
			return false, fmt.Errorf("service %s failed to converge: %s", name, progress.Message)
		}
		if progress.State == ServiceReady {
			state.converged++
			continue
		}
		allReady = false
	}
	return allReady, nil
}

func finishServiceWaitSpan(span trace.Span, startedAt time.Time, state waitState, waitErr error) {
	outcome := "success"
	if waitErr != nil {
		outcome = "failed"
		tracing.FailSpan(span, waitErr)
		span.SetAttributes(attribute.String(
			"swarm-deploy.deployment.service_wait.failure_reason",
			boundedDiagnostic(waitErr.Error()),
		))
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.SetAttributes(
		attribute.Int64("swarm-deploy.deployment.service_wait.duration_ms", time.Since(startedAt).Milliseconds()),
		attribute.Int("swarm-deploy.deployment.service_wait.observed_services", state.observed),
		attribute.Int("swarm-deploy.deployment.service_wait.converged_services", state.converged),
		attribute.String("swarm-deploy.deployment.service_wait.outcome", outcome),
	)
}

type waitState struct {
	last      map[string]ServiceProgress
	observed  int
	converged int
}

func (s *waitState) emit(span trace.Span, callback ServiceProgressCallback, progress ServiceProgress) {
	if previous, ok := s.last[progress.Name]; ok && previous == progress {
		return
	}
	s.last[progress.Name] = progress
	span.AddEvent("service convergence state changed", trace.WithAttributes(
		tracing.ResourceServiceID.String(progress.ServiceID),
		tracing.ResourceServiceName.String(progress.Name),
		attribute.String("swarm-deploy.deployment.service.state", string(progress.State)),
		attribute.String("swarm-deploy.deployment.service.desired_tasks", strconv.FormatUint(progress.Desired, 10)),
		attribute.String("swarm-deploy.deployment.service.running_tasks", strconv.FormatUint(progress.Running, 10)),
		attribute.String("swarm-deploy.deployment.service.message", progress.Message),
	))
	if callback != nil {
		callback(progress)
	}
}

func (s *waitState) failPending(span trace.Span, callback ServiceProgressCallback, cause error) {
	for _, name := range sortedProgressNames(s.last) {
		progress := s.last[name]
		if progress.State != ServicePending {
			continue
		}
		progress.State = ServiceFailed
		progress.Message = boundedDiagnostic(fmt.Sprintf("wait failed: %v; last state: %s", cause, progress.Message))
		s.emit(span, callback, progress)
	}
}

func (w *ServiceWaiter) observeService(
	ctx context.Context,
	name string,
	baseline swarm.StackService,
	service swarm.StackService,
) (ServiceProgress, error) {
	if service.ID == "" {
		return ServiceProgress{Name: name, State: ServicePending, Message: "waiting for service to appear"}, nil
	}

	desired := serviceDesiredTasks(service)
	progress := ServiceProgress{ServiceID: service.ID, Name: name, State: ServicePending, Desired: desired}
	if message, failed := terminalUpdateFailure(service.UpdateStatus); failed {
		progress.State = ServiceFailed
		progress.Message = message
		return progress, nil
	}
	if service.Mode == "replicated-job" || service.Mode == "global-job" {
		progress.State = ServiceReady
		progress.Message = "service job mode is outside convergence monitoring scope"
		return progress, nil
	}
	if service.Mode != "replicated" && service.Mode != "global" {
		progress.State = ServiceFailed
		progress.Message = boundedDiagnostic(fmt.Sprintf("unsupported service mode %q", service.Mode))
		return progress, nil
	}

	tasks, err := w.services.ListTasks(ctx, service.ID)
	if err != nil {
		return progress, fmt.Errorf("list tasks for service %s: %w", name, err)
	}

	requireCurrentGeneration := baseline.ID == "" || baseline.ID != service.ID ||
		!reflect.DeepEqual(baseline.ServiceSpec.TaskTemplate, service.ServiceSpec.TaskTemplate)
	cutoff, generationPending := taskGenerationCutoff(baseline, service, requireCurrentGeneration)
	if generationPending {
		progress.Message = "waiting for current rolling update status"
		return progress, nil
	}

	progress.Running = countCurrentRunningTasks(tasks, requireCurrentGeneration, cutoff)
	if updatePending(service.UpdateStatus) {
		progress.Message = updateProgressMessage(service.UpdateStatus)
		return progress, nil
	}
	if progress.Running == desired && countDesiredTasks(tasks) == desired {
		progress.State = ServiceReady
		progress.Message = "service converged"
		return progress, nil
	}

	progress.Message = boundedDiagnostic(fmt.Sprintf(
		"waiting for current tasks: %d/%d running%s",
		progress.Running,
		desired,
		taskFailureDiagnostic(tasks),
	))
	return progress, nil
}

func initialTargets(
	candidates []string,
	baseline map[string]swarm.StackService,
	onlyAffected bool,
) map[string]struct{} {
	targets := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		if !onlyAffected || baseline[name].ID == "" {
			targets[name] = struct{}{}
		}
	}
	return targets
}

func discoverAffectedTargets(
	targets map[string]struct{},
	candidates []string,
	baseline map[string]swarm.StackService,
	current map[string]swarm.StackService,
) {
	for _, name := range candidates {
		before, existed := baseline[name]
		if !existed {
			targets[name] = struct{}{}
			continue
		}
		after, visible := current[name]
		if !visible || serviceChanged(before, after) {
			targets[name] = struct{}{}
		}
	}
}

func serviceChanged(before, after swarm.StackService) bool {
	return before.ID != after.ID || !before.UpdatedAt.Equal(after.UpdatedAt) ||
		!reflect.DeepEqual(before.ServiceSpec, after.ServiceSpec)
}

func servicesByName(services []swarm.StackService) map[string]swarm.StackService {
	mapped := make(map[string]swarm.StackService, len(services))
	for _, service := range services {
		mapped[service.Name] = service
	}
	return mapped
}

func serviceDesiredTasks(service swarm.StackService) uint64 {
	if service.Mode == "replicated" && service.Replicas != nil {
		return *service.Replicas
	}
	return service.DesiredTasks
}

func countDesiredTasks(tasks []swarm.ServiceTask) uint64 {
	var count uint64
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning {
			count++
		}
	}
	return count
}

func countCurrentRunningTasks(tasks []swarm.ServiceTask, requireCurrentGeneration bool, cutoff time.Time) uint64 {
	var count uint64
	for _, task := range tasks {
		if task.DesiredState != swarm.TaskStateRunning || task.CurrentState != swarm.TaskStateRunning {
			continue
		}
		if requireCurrentGeneration && task.CreatedAt.Before(cutoff) {
			continue
		}
		count++
	}
	return count
}

func taskGenerationCutoff(
	baseline swarm.StackService,
	service swarm.StackService,
	requireCurrentGeneration bool,
) (time.Time, bool) {
	if !requireCurrentGeneration {
		return time.Time{}, false
	}
	if baseline.ID == "" || baseline.ID != service.ID {
		return service.CreatedAt, false
	}
	if service.UpdateStatus == nil || service.UpdateStatus.StartedAt.IsZero() ||
		!service.UpdateStatus.StartedAt.After(baseline.UpdatedAt) {
		return time.Time{}, true
	}
	return service.UpdateStatus.StartedAt, false
}

func terminalUpdateFailure(status *swarm.ServiceUpdateStatus) (string, bool) {
	if status == nil {
		return "", false
	}
	switch status.State {
	case "paused", "rollback_started", "rollback_paused", "rollback_completed":
		message := "service update is " + status.State
		if diagnostic := boundedDiagnostic(status.Message); diagnostic != "" {
			message += ": " + diagnostic
		}
		return message, true
	default:
		return "", false
	}
}

func updatePending(status *swarm.ServiceUpdateStatus) bool {
	if status == nil {
		return false
	}
	switch status.State {
	case "", "paused", "rollback_started", "rollback_paused", "rollback_completed":
		return false
	case "updating":
		return true
	case "completed":
		return false
	default:
		return true
	}
}

func updateProgressMessage(status *swarm.ServiceUpdateStatus) string {
	message := "waiting for rolling update to complete"
	if status != nil && boundedDiagnostic(status.Message) != "" {
		message += ": " + boundedDiagnostic(status.Message)
	}
	return message
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
	reason := boundedDiagnostic(failed[0].Error)
	if reason == "" {
		reason = string(failed[0].CurrentState)
	}
	return "; latest task failure: " + reason
}

func waitInterruptedError(stackName string, progress map[string]ServiceProgress, cause error) error {
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
	return fmt.Errorf("wait for services in stack %s: %w: %s", stackName, cause, strings.Join(pending, "; "))
}

func uniqueSortedNames(names []string) []string {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	return sortedTargetNames(set)
}

func sortedTargetNames(names map[string]struct{}) []string {
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return sorted
}

func sortedProgressNames(progress map[string]ServiceProgress) []string {
	names := make([]string, 0, len(progress))
	for name := range progress {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func boundedDiagnostic(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxServiceDiagnosticLength {
		return message
	}
	return message[:maxServiceDiagnosticLength] + "..."
}
