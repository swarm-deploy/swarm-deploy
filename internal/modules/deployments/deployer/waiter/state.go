// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func finishServiceWaitSpan(span trace.Span, startedAt time.Time, state waitState, waitErr error) {
	outcome := "success"
	if waitErr != nil {
		outcome = "failed"
		tracing.FailSpan(span, waitErr)
		span.SetAttributes(tracing.DeploymentServiceWaitFailureReason.String(
			boundedDiagnostic(waitErr.Error()),
		))
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.SetAttributes(
		tracing.DeploymentServiceWaitDurationMS.Int64(time.Since(startedAt).Milliseconds()),
		tracing.DeploymentServiceWaitObservedServices.Int(state.observed),
		tracing.DeploymentServiceWaitConvergedServices.Int(state.converged),
		tracing.DeploymentServiceWaitOutcome.String(outcome),
	)
}

type waitState struct {
	targets              map[string]struct{}
	last                 map[string]ServiceProgress
	services             map[string]swarm.StackService
	observed             int
	converged            int
	lastObservationError error
}

func (s *waitState) observe(
	span trace.Span, callback ServiceProgressCallback, service swarm.StackService, progress ServiceProgress,
) {
	previous := s.last[progress.Name]
	if progress.taskLookupFailed && !s.serviceChanged(progress.Name, service) {
		// A failed lookup adds no new task evidence for the same service revision.
		switch {
		case previous.State == ServiceFailed && progress.State == ServicePending:
			progress = previous
		case previous.State == progress.State:
			progress.FailedTaskID = previous.FailedTaskID
			progress.taskFault = previous.taskFault
			progress.taskContext = previous.taskContext
			progress.placementConstraints = previous.placementConstraints
		}
	}
	s.services[progress.Name] = service
	s.emit(span, callback, progress)
}

func (s *waitState) serviceChanged(name string, service swarm.StackService) bool {
	previous := s.services[name]
	return serviceChanged(previous, service) || !reflect.DeepEqual(previous.UpdateStatus, service.UpdateStatus)
}

func (s *waitState) invalidateChangedService(
	span trace.Span, callback ServiceProgressCallback, name string, service swarm.StackService,
) {
	if _, observed := s.last[name]; observed && s.serviceChanged(name, service) {
		s.observe(span, callback, service, ServiceProgress{
			Name: name, ServiceID: service.ID, State: ServicePending, Message: "waiting for current service observation",
		})
	}
}

func (s *waitState) emit(span trace.Span, callback ServiceProgressCallback, progress ServiceProgress) {
	// Faults are reconstructed on each poll, so compare their values rather than pointer identity.
	if previous, ok := s.last[progress.Name]; ok && reflect.DeepEqual(previous, progress) {
		return
	}
	s.last[progress.Name] = progress
	span.AddEvent("service convergence state changed", trace.WithAttributes(
		tracing.ResourceServiceID.String(progress.ServiceID),
		tracing.ResourceServiceName.String(progress.Name),
		tracing.DeploymentServiceState.String(string(progress.State)),
		tracing.DeploymentServiceDesiredTasks.Int64(int64(progress.Desired)), //nolint:gosec // Docker task counts fit int64
		tracing.DeploymentServiceRunningTasks.Int64(int64(progress.Running)), //nolint:gosec // Docker task counts fit int64
		tracing.DeploymentServiceMessage.String(progress.Message),
		tracing.DeploymentServiceSpecificationOnly.Bool(progress.SpecificationOnly),
	))
	if callback != nil {
		callback(progress)
	}
}

func (s *waitState) terminalError() error {
	var failures []error
	for _, name := range sortedProgressNames(s.last) {
		progress := s.last[name]
		if progress.State != ServiceFailed {
			continue
		}
		if progress.taskFault != nil {
			failures = append(failures, fmt.Errorf("service %s failed to converge: %s: %w",
				name, progress.taskContext, progress.taskFault))
		} else {
			failures = append(failures, fmt.Errorf("service %s failed to converge: %s", name, progress.Message))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrConvergenceFailed, errors.Join(failures...))
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

func (s *waitState) retryPending(
	span trace.Span,
	callback ServiceProgressCallback,
	targets map[string]struct{},
	cause error,
) {
	s.lastObservationError = cause
	for _, name := range sortedTargetNames(targets) {
		progress, ok := s.last[name]
		if ok && progress.State != ServicePending {
			continue
		}
		if !ok {
			progress = ServiceProgress{Name: name, State: ServicePending}
		}
		progress.Message = boundedDiagnostic(fmt.Sprintf("temporary Docker API error; retrying: %v", cause))
		s.emit(span, callback, progress)
	}
}

func (s *waitState) result() ServiceWaitResult {
	result := ServiceWaitResult{}
	for _, name := range sortedTargetNames(s.targets) {
		progress := s.last[name]
		if progress.State == ServiceFailed && progress.FailedTaskID != "" {
			result.FailedTasks = append(result.FailedTasks, progress)
		}
		if progress.State != ServiceReady {
			result.UnresolvedServices = append(result.UnresolvedServices, name)
		} else if progress.SpecificationOnly {
			result.SpecificationOnlyServices = append(result.SpecificationOnlyServices, name)
		}
	}
	return result
}
