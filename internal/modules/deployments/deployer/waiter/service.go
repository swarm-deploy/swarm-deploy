// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultServiceWaitTimeout  = 10 * time.Minute
	maxServiceDiagnosticLength = 256
	globalServiceMode          = "global"
	replicatedServiceMode      = "replicated"
)

var serviceWaiterTracer = otel.Tracer(
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/servicewaiter",
)

// ErrConvergenceFailed identifies an observed terminal Swarm failure, rather than an observation error.
var ErrConvergenceFailed = errors.New("service convergence failed")

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

// ServiceWaitResult contains the final verification obligations, including targets never observed.
type ServiceWaitResult struct {
	// UnresolvedServices contains every target whose last observed state was not ready.
	UnresolvedServices []string
}

// ServiceWaitRequest defines the services to observe after a detached stack apply.
type ServiceWaitRequest struct {
	// StackName is the Docker stack namespace.
	StackName string
	// ServiceNames is the complete verification target set, without the stack prefix.
	// The caller selects changed services and prior unresolved rollouts.
	ServiceNames []string
	// Baseline is the stack service snapshot captured immediately before apply.
	Baseline []swarm.StackService
	// AppliedServices contains services actually created or updated by this apply.
	// These must become visible before their tasks or update status can satisfy verification.
	AppliedServices []string
	// UnresolvedServices requires proof of the rollout generation even without a new apply.
	UnresolvedServices []string
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

// Wait blocks until the requested services converge or the wait is interrupted.
func (w *ServiceWaiter) Wait(ctx context.Context, request ServiceWaitRequest) (ServiceWaitResult, error) {
	startedAt := time.Now()
	waitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	waitCtx, span := serviceWaiterTracer.Start(waitCtx, "serviceWaiter.Wait", trace.WithAttributes(
		tracing.ResourceStackName.String(request.StackName),
	))
	defer span.End()

	state := waitState{last: make(map[string]ServiceProgress)}
	var waitErr error
	defer func() {
		finishServiceWaitSpan(span, startedAt, state, waitErr)
	}()

	waitErr = w.wait(waitCtx, span, request, &state)
	return state.result(), waitErr
}

func (w *ServiceWaiter) wait(
	ctx context.Context,
	span trace.Span,
	request ServiceWaitRequest,
	state *waitState,
) error {

	baseline := servicesByName(request.Baseline)
	candidates := uniqueSortedNames(request.ServiceNames)
	targets := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		targets[name] = struct{}{}
	}
	state.targets = targets
	if len(candidates) == 0 {
		return nil
	}
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		allReady, observeErr := w.poll(ctx, span, request, baseline, targets, state)
		switch {
		case errors.Is(observeErr, ErrConvergenceFailed):
			// Finish observing independent targets before returning the known failure.
			state.terminalFailure = observeErr
			if allReady {
				return observeErr
			}
		case observeErr != nil:
			if waitErr := state.handleObservationError(ctx, span, request, targets, observeErr); waitErr != nil {
				return errors.Join(state.terminalFailure, waitErr)
			}
		case allReady:
			return state.terminalFailure
		}

		select {
		case <-ctx.Done():
			state.failPending(span, request.Progress, ctx.Err())
			return errors.Join(state.terminalFailure,
				waitInterruptedError(request.StackName, state.last, ctx.Err(), state.lastObservationError))
		case <-ticker.C:
		}
	}
}

func (w *ServiceWaiter) poll(
	ctx context.Context,
	span trace.Span,
	request ServiceWaitRequest,
	baseline map[string]swarm.StackService,
	targets map[string]struct{},
	state *waitState,
) (bool, error) {
	current, err := w.Snapshot(ctx, request.StackName)
	if err != nil {
		return false, err
	}
	currentByName := servicesByName(current)
	if len(targets) == 0 {
		return true, nil
	}

	return w.observeTargets(ctx, span, request, baseline, currentByName, targets, state)
}

func (w *ServiceWaiter) observeTargets(
	ctx context.Context,
	span trace.Span,
	request ServiceWaitRequest,
	baseline map[string]swarm.StackService,
	current map[string]swarm.StackService,
	targets map[string]struct{},
	state *waitState,
) (bool, error) {
	state.observed = len(targets)
	state.converged = 0
	allReady := true
	var failures []error
	for _, name := range sortedTargetNames(targets) {
		if slices.Contains(request.AppliedServices, name) && baseline[name].ID != "" &&
			!serviceChanged(baseline[name], current[name]) {
			// Docker may briefly return the pre-apply snapshot. Neither old running
			// tasks nor an old paused update describes the accepted service version.
			state.emit(span, request.Progress, ServiceProgress{
				Name: name, State: ServicePending, Message: "waiting for applied service version",
			})
			allReady = false
			continue
		}
		progress, err := w.observeService(ctx, name, baseline[name], current[name],
			slices.Contains(request.UnresolvedServices, name))
		state.emit(span, request.Progress, progress)
		if err != nil {
			return false, errors.Join(append(failures, err)...)
		}
		if progress.State == ServiceFailed {
			failures = append(failures, fmt.Errorf("%w: service %s failed to converge: %s",
				ErrConvergenceFailed, name, progress.Message))
			continue
		}
		if progress.State == ServiceReady {
			state.converged++
			continue
		}
		allReady = false
	}
	return allReady, errors.Join(failures...)
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

func isTransientDockerAPIError(err error) bool {
	if cerrdefs.IsUnavailable(err) {
		return true
	}

	var temporary faults.Error
	if errors.As(err, &temporary) && temporary.Temporary() {
		return true
	}

	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
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
