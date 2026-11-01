// Package waiter observes Docker Swarm service convergence after detached applies.
package waiter

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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
	targets              map[string]struct{}
	last                 map[string]ServiceProgress
	observed             int
	converged            int
	lastObservationError error
	terminalFailure      error
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

func (s *waitState) handleObservationError(
	ctx context.Context,
	span trace.Span,
	request ServiceWaitRequest,
	targets map[string]struct{},
	cause error,
) error {
	if ctx.Err() != nil {
		s.failPending(span, request.Progress, ctx.Err())
		return waitInterruptedError(request.StackName, s.last, ctx.Err(), s.lastObservationError)
	}
	if isTransientDockerAPIError(cause) {
		s.retryPending(span, request.Progress, targets, cause)
		return nil
	}
	s.failPending(span, request.Progress, cause)
	return cause
}

func (s *waitState) result() ServiceWaitResult {
	result := ServiceWaitResult{}
	for _, name := range sortedTargetNames(s.targets) {
		if s.last[name].State != ServiceReady {
			result.UnresolvedServices = append(result.UnresolvedServices, name)
		}
	}
	return result
}
