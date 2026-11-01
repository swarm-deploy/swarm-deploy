package deployments

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// CompletionOutcome identifies the terminal transition selected by the orchestrator.
type CompletionOutcome string

const (
	// CompletionSucceeded records a successful apply and cleanup.
	CompletionSucceeded CompletionOutcome = "succeeded"
	// CompletionApplyFailed records a known apply-stage failure.
	CompletionApplyFailed CompletionOutcome = "apply_failed"
	// CompletionPolicyRejected records an image-policy rejection before apply.
	CompletionPolicyRejected CompletionOutcome = "policy_rejected"
	// CompletionInitJobFailed records an Init Job failure before stack apply.
	CompletionInitJobFailed CompletionOutcome = "init_failed"
	// CompletionConvergenceFailed records a known terminal service update failure.
	CompletionConvergenceFailed CompletionOutcome = "convergence_failed"
	// CompletionVerificationInterrupted records an unknown verification outcome.
	CompletionVerificationInterrupted CompletionOutcome = "verification_interrupted"
	// CompletionCleanupFailed records cleanup failure after observing live state.
	CompletionCleanupFailed CompletionOutcome = "cleanup_failed"
	// CompletionProcessInterrupted records recovery of an unfinished process attempt.
	CompletionProcessInterrupted CompletionOutcome = "process_interrupted"
)

// Completion describes the terminal outcome selected by a deployment orchestrator.
type Completion struct {
	// UnresolvedServices contains applied services not proven converged by this execution.
	// Nil on failure conservatively retains every desired service for legacy/unknown outcomes.
	UnresolvedServices []string
	// Outcome identifies the terminal transition.
	Outcome CompletionOutcome
	// Diagnostic contains optional technical context and is sanitized before persistence or publication.
	Diagnostic error
	// ObservedAt records when post-apply Swarm state was read successfully.
	ObservedAt *time.Time
}

type completionState struct {
	unresolvedServices []string
	status             deploymentmodel.Status
	errorCode          string
	diagnostic         error
	retryable          bool
	phase              deploymentmodel.Phase
	apply              deploymentmodel.StageStatus
	verification       deploymentmodel.StageStatus
	cleanup            deploymentmodel.StageStatus
	actual             deploymentmodel.ActualStateStatus
	observedAt         *time.Time
	eventStage         string
}

// Complete persists one terminal attempt transition, updates the successful baseline when applicable,
// and publishes the deployment event in one database transaction.
func (s *Service) Complete(
	ctx context.Context,
	attemptID string,
	completion Completion,
) error {
	state, err := completion.state()
	if err != nil {
		return err
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		return s.complete(ctx, attemptID, state)
	})
}

func (s *Service) complete(
	ctx context.Context,
	attemptID string,
	state completionState,
) error {
	attempt, err := s.store.GetAttempt(ctx, attemptID)
	if err != nil {
		return err
	}
	deployment, err := s.store.Get(ctx, attempt.DeploymentID)
	if err != nil {
		return err
	}
	if attempt.Status != deploymentmodel.Running ||
		deployment.Status != deploymentmodel.Running ||
		deployment.LatestAttemptID != attempt.ID {
		return deploymentstore.ErrNotRunning
	}
	desired, err := s.store.Desired(ctx, deployment.ID)
	if err != nil {
		return err
	}

	finishedAt := s.now().UTC()
	attempt.Status, attempt.FinishedAt, attempt.ErrorCode = state.status, &finishedAt, state.errorCode
	attempt.ErrorDiagnostic = faults.SafeDiagnostic(state.diagnostic)
	attempt.Retryable = state.retryable
	attempt.Phase, attempt.ApplyStatus = state.phase, state.apply
	attempt.VerificationStatus, attempt.CleanupStatus = state.verification, state.cleanup
	attempt.ActualStateStatus, attempt.ObservedAt = state.actual, state.observedAt
	if state.status == deploymentmodel.Succeeded {
		deployment.UnresolvedServices = nil
	} else {
		stateInheritedServices := deployment.UnresolvedServices
		unresolved := state.unresolvedServices
		if unresolved == nil {
			unresolved = desiredServiceNames(desired)
		}
		deployment.UnresolvedServices = append([]string{}, unresolved...)
		if state.phase == deploymentmodel.PhaseApply {
			deployment.UnresolvedServices = append(deployment.UnresolvedServices, stateInheritedServices...)
		}
		slices.Sort(deployment.UnresolvedServices)
		deployment.UnresolvedServices = slices.Compact(deployment.UnresolvedServices)
	}
	if err = s.store.FinishAttempt(ctx, deployment, attempt); err != nil {
		return err
	}
	if state.status == deploymentmodel.Succeeded {
		if err = s.store.SaveBaseline(ctx, deployment.ID); err != nil {
			return err
		}
	}
	return s.publishCompletion(ctx, deployment, attempt, desired, state)
}

func (completion Completion) state() (completionState, error) {
	state := completionState{
		unresolvedServices: completion.rolloutObligations(),
		errorCode:          completion.ErrorCode(),
		diagnostic:         faults.SafeError(completion.Diagnostic),
		observedAt:         completion.ObservedAt,
	}
	// Persist retry eligibility without changing the stable public failure codes.
	switch completion.Outcome {
	case CompletionVerificationInterrupted, CompletionProcessInterrupted:
		state.retryable = true
	case CompletionApplyFailed, CompletionInitJobFailed, CompletionCleanupFailed:
		var temporary faults.Error
		state.retryable = errors.Is(completion.Diagnostic, context.DeadlineExceeded) ||
			(errors.As(completion.Diagnostic, &temporary) && temporary.Temporary())
	case CompletionSucceeded, CompletionPolicyRejected, CompletionConvergenceFailed:
		// These outcomes never trigger an automatic retry.
	}
	switch completion.Outcome {
	case CompletionSucceeded:
		if completion.Diagnostic != nil {
			return completionState{}, fmt.Errorf("successful completion cannot contain an error")
		}
		state.status, state.phase = deploymentmodel.Succeeded, deploymentmodel.PhaseCompleted
		state.apply, state.verification = deploymentmodel.StageSucceeded, deploymentmodel.StageSkipped
		state.cleanup = deploymentmodel.StageSucceeded
		state.actual = deploymentmodel.ActualStateUnknown
		if completion.ObservedAt != nil {
			state.actual = deploymentmodel.ActualStateObserved
		}
	case CompletionApplyFailed, CompletionPolicyRejected, CompletionInitJobFailed:
		if completion.ObservedAt != nil {
			return completionState{}, fmt.Errorf("apply failure cannot contain an observation timestamp")
		}
		state.status, state.phase = deploymentmodel.Failed, deploymentmodel.PhaseApply
		state.apply, state.verification = deploymentmodel.StageFailed, deploymentmodel.StageSkipped
		state.cleanup, state.actual = deploymentmodel.StageSkipped, deploymentmodel.ActualStateUnknown
		state.eventStage = string(state.phase)
	case CompletionConvergenceFailed, CompletionVerificationInterrupted:
		if completion.ObservedAt != nil {
			return completionState{}, fmt.Errorf("verification failure cannot contain an observation timestamp")
		}
		state.status, state.phase = deploymentmodel.Interrupted, deploymentmodel.PhaseVerification
		state.apply, state.verification = deploymentmodel.StageSucceeded, deploymentmodel.StageUnknown
		state.cleanup, state.actual = deploymentmodel.StageSkipped, deploymentmodel.ActualStateUnknown
		if completion.Outcome == CompletionConvergenceFailed {
			state.status, state.verification = deploymentmodel.Failed, deploymentmodel.StageFailed
		}
		state.eventStage = string(state.phase)
	case CompletionCleanupFailed:
		if completion.ObservedAt == nil {
			return completionState{}, fmt.Errorf("cleanup failure requires an observation timestamp")
		}
		state.status, state.phase = deploymentmodel.Failed, deploymentmodel.PhaseCleanup
		state.apply, state.verification = deploymentmodel.StageSucceeded, deploymentmodel.StageSkipped
		state.cleanup, state.actual = deploymentmodel.StageFailed, deploymentmodel.ActualStateObserved
		state.eventStage = string(state.phase)
	case CompletionProcessInterrupted:
		if completion.ObservedAt != nil {
			return completionState{}, fmt.Errorf("process interruption cannot contain an observation timestamp")
		}
		state.status, state.phase = deploymentmodel.Interrupted, deploymentmodel.PhaseApply
		state.apply, state.verification = deploymentmodel.StageUnknown, deploymentmodel.StageSkipped
		state.cleanup, state.actual = deploymentmodel.StageSkipped, deploymentmodel.ActualStateUnknown
	default:
		return completionState{}, fmt.Errorf("unsupported completion outcome %q", completion.Outcome)
	}
	return state, nil
}

// ErrorCode returns the stable terminal error category for the selected outcome.
func (completion Completion) ErrorCode() string {
	switch completion.Outcome {
	case CompletionSucceeded:
		return ""
	case CompletionApplyFailed:
		return "apply_failed"
	case CompletionPolicyRejected:
		return "policy_rejected"
	case CompletionInitJobFailed:
		return "init_failed"
	case CompletionConvergenceFailed:
		return "convergence_failed"
	case CompletionVerificationInterrupted:
		return "verification_unknown"
	case CompletionCleanupFailed:
		return "prune_failed"
	case CompletionProcessInterrupted:
		return "process_interrupted"
	default:
		return ""
	}
}

func (s *Service) publishCompletion(
	ctx context.Context,
	deployment deploymentmodel.Deployment,
	attempt deploymentmodel.Attempt,
	desired compose.File,
	state completionState,
) error {
	meta := events.DeployEvent{
		DeploymentID: deployment.ID, AttemptID: attempt.ID, StackName: deployment.Stack, Commit: deployment.Commit,
		Services: desired.Compose.Services, StackDefinition: desired,
	}
	switch state.status {
	case deploymentmodel.Succeeded:
		return s.events.Publish(ctx, &events.DeploySuccess{DeployEvent: meta})
	case deploymentmodel.Failed:
		return s.events.Publish(ctx, &events.DeployFailed{
			DeployEvent: meta, Error: state.diagnostic, ErrorCode: state.errorCode, Stage: state.eventStage,
		})
	case deploymentmodel.Interrupted:
		return s.events.Publish(ctx, &events.DeployInterrupted{
			DeploymentID: deployment.ID, AttemptID: attempt.ID, StackName: deployment.Stack,
			Commit: deployment.Commit, Services: desired.Compose.Services, Error: state.diagnostic,
			Reason: state.errorCode, Stage: state.eventStage,
		})
	case deploymentmodel.Running, deploymentmodel.Superseded:
		return fmt.Errorf("unsupported completion status %q", state.status)
	default:
		return fmt.Errorf("unsupported completion status %q", state.status)
	}
}

func (completion Completion) rolloutObligations() []string {
	// A failed pre-apply check has not created a new failed service generation.
	// Keep inherited obligations, but do not require replacement of healthy tasks.
	if completion.UnresolvedServices == nil &&
		(completion.Outcome == CompletionInitJobFailed || completion.Outcome == CompletionPolicyRejected) {
		return []string{}
	}
	return completion.UnresolvedServices
}
