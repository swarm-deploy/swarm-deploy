package deployments

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
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
	// Verification contains the rollout obligations known at completion.
	Verification deploymentmodel.Verification
	// Outcome identifies the terminal transition.
	Outcome CompletionOutcome
	// Diagnostic contains optional technical context and is sanitized before persistence or publication.
	Diagnostic error
	// Retryable explicitly permits automatic retry of an apply failure before DeployStack is invoked.
	Retryable bool
	// ObservedAt records when post-apply Swarm state was read successfully.
	ObservedAt *time.Time
	// AppliedServices lists targeted services confirmed by a successful partial or complete execution.
	// Nil together with SkippedServices defaults to every pending service for compatibility.
	AppliedServices []string
	// SkippedServices lists targeted services deliberately left pending.
	SkippedServices []string
	// ConfirmedRemovals lists pending removals confirmed absent after successful cleanup.
	// Nil together with SkippedRemovals defaults to every pending removal for compatibility.
	ConfirmedRemovals []string
	// SkippedRemovals lists services whose removal remains pending.
	SkippedRemovals []string
	// PrunedServices lists services actually removed by successful cleanup.
	PrunedServices []string
	// StackResourcesApplied explicitly controls stack-resource confirmation when non-nil.
	StackResourcesApplied *bool
}

type completionState struct {
	rolloutVerification   deploymentmodel.Verification
	status                deploymentmodel.Status
	errorCode             string
	diagnostic            error
	retryable             bool
	phase                 deploymentmodel.Phase
	apply                 deploymentmodel.StageStatus
	verification          deploymentmodel.StageStatus
	cleanup               deploymentmodel.StageStatus
	actual                deploymentmodel.ActualStateStatus
	observedAt            *time.Time
	eventStage            string
	appliedServices       []string
	skippedServices       []string
	confirmedRemovals     []string
	skippedRemovals       []string
	prunedServices        []string
	stackResourcesApplied *bool
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

	prepared, err := s.store.Prepared(ctx, deployment.ID)
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
		deployment.Verification = deploymentmodel.Verification{Known: true}
	} else {
		stateInheritedServices := deployment.Verification.UnresolvedServices
		unresolved := state.rolloutVerification.UnresolvedServices
		if !state.rolloutVerification.Known {
			unresolved = desiredServiceNames(desired)
		}
		deployment.Verification.Known = true
		deployment.Verification.UnresolvedServices = slices.Clone(unresolved)
		if state.phase == deploymentmodel.PhaseApply {
			deployment.Verification.UnresolvedServices = append(
				deployment.Verification.UnresolvedServices, stateInheritedServices...,
			)
		}
		slices.Sort(deployment.Verification.UnresolvedServices)
		deployment.Verification.UnresolvedServices = slices.Compact(deployment.Verification.UnresolvedServices)
		deployment.Verification.Generations = unresolvedGenerations(
			deployment.Verification, deployment.Verification.UnresolvedServices,
		)
	}
	fullyApplied := false
	if state.status == deploymentmodel.Succeeded {
		fullyApplied, err = s.confirmSuccessfulState(ctx, deployment, &attempt, prepared, state)
		if err != nil {
			return err
		}
	} else if err = s.store.MarkAppliedUncertain(
		ctx, deployment.Stack, deployment.PendingServices, deployment.StackResourcesPending,
	); err != nil {
		return err
	}
	if err = s.store.FinishAttempt(ctx, deployment, attempt); err != nil {
		return err
	}
	if state.status == deploymentmodel.Succeeded && fullyApplied {
		if err = s.store.SaveBaseline(ctx, deployment.ID); err != nil {
			return err
		}
	}
	return s.publishCompletion(ctx, deployment, attempt, desired, state)
}

func (s *Service) confirmSuccessfulState(
	ctx context.Context,
	deployment deploymentmodel.Deployment,
	attempt *deploymentmodel.Attempt,
	prepared deploymentmodel.Prepared,
	state completionState,
) (bool, error) {
	applied, skipped, removals, skippedRemovals, resourcesApplied, err :=
		resolveConfirmation(deployment, state)
	if err != nil {
		return false, err
	}
	hashState, err := s.store.ConfirmAppliedState(
		ctx, deployment.Stack, deployment.Commit, prepared, applied, removals, resourcesApplied,
	)
	if err != nil {
		return false, err
	}
	attempt.AppliedServices, attempt.SkippedServices = applied, skipped
	attempt.ConfirmedRemovals, attempt.SkippedRemovals = removals, skippedRemovals
	attempt.PrunedServices = slices.Clone(state.prunedServices)
	attempt.ConfirmedServiceDigests = make(map[string]string, len(applied))
	for _, name := range applied {
		attempt.ConfirmedServiceDigests[name] = prepared.ServiceDigests[name]
	}
	if resourcesApplied {
		attempt.ConfirmedStackResourcesDigest = prepared.StackResourcesDigest
	}
	fullyApplied := hashState.AppliedDigest == prepared.Digest &&
		hashState.AppliedVersion == prepared.DigestVersion
	if fullyApplied {
		attempt.ConfirmedStackDigest = prepared.Digest
	}
	return fullyApplied, nil
}

func (completion Completion) state() (completionState, error) {
	state := completionState{
		rolloutVerification:   completion.rolloutObligations(),
		errorCode:             completion.ErrorCode(),
		diagnostic:            faults.SafeError(completion.Diagnostic),
		observedAt:            completion.ObservedAt,
		appliedServices:       completion.AppliedServices,
		skippedServices:       completion.SkippedServices,
		confirmedRemovals:     completion.ConfirmedRemovals,
		skippedRemovals:       completion.SkippedRemovals,
		prunedServices:        completion.PrunedServices,
		stackResourcesApplied: completion.StackResourcesApplied,
	}
	// Persist retry eligibility without changing the stable public failure codes.
	switch completion.Outcome {
	case CompletionVerificationInterrupted, CompletionProcessInterrupted:
		state.retryable = true
	case CompletionApplyFailed, CompletionInitJobFailed, CompletionCleanupFailed:
		var temporary faults.Error
		// An update deferred only by active external tasks succeeds once they finish.
		state.retryable = (completion.Outcome == CompletionApplyFailed && completion.Retryable) ||
			errors.Is(completion.Diagnostic, context.DeadlineExceeded) ||
			errors.Is(completion.Diagnostic, services.ErrUpdateDeferred) ||
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

func resolveConfirmation(
	deployment deploymentmodel.Deployment,
	state completionState,
) ([]string, []string, []string, []string, bool, error) {
	applied, skipped, err := confirmationPartition(
		deployment.PendingServices, state.appliedServices, state.skippedServices, "service",
	)
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	removals, skippedRemovals, err := confirmationPartition(
		deployment.PendingRemovals, state.confirmedRemovals, state.skippedRemovals, "removal",
	)
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	resourcesApplied := deployment.StackResourcesPending
	if state.stackResourcesApplied != nil {
		resourcesApplied = *state.stackResourcesApplied
	}
	if resourcesApplied && !deployment.StackResourcesPending {
		return nil, nil, nil, nil, false, fmt.Errorf("stack resources were not pending for deployment")
	}
	return applied, skipped, removals, skippedRemovals, resourcesApplied, nil
}

func confirmationPartition(
	target []string,
	confirmed []string,
	skipped []string,
	kind string,
) ([]string, []string, error) {
	if confirmed == nil && skipped == nil {
		return slices.Clone(target), []string{}, nil
	}
	confirmed = slices.Clone(confirmed)
	skipped = slices.Clone(skipped)
	slices.Sort(confirmed)
	slices.Sort(skipped)
	if len(slices.Compact(confirmed)) != len(confirmed) || len(slices.Compact(skipped)) != len(skipped) {
		return nil, nil, fmt.Errorf("duplicate %s confirmation", kind)
	}
	actual := append(slices.Clone(confirmed), skipped...)
	slices.Sort(actual)
	expected := slices.Clone(target)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		return nil, nil, fmt.Errorf("%s confirmation does not match pending target", kind)
	}
	return confirmed, skipped, nil
}

// ErrorCode returns the stable terminal error category for the selected outcome.
func (completion Completion) ErrorCode() string {
	switch completion.Outcome {
	case CompletionSucceeded:
		return ""
	case CompletionApplyFailed:
		return events.DeployErrorCodeApplyFailed
	case CompletionPolicyRejected:
		return events.DeployErrorCodePolicyRejected
	case CompletionInitJobFailed:
		return events.DeployErrorCodeInitFailed
	case CompletionConvergenceFailed:
		return events.DeployErrorCodeConvergenceFailed
	case CompletionVerificationInterrupted:
		return events.DeployInterruptReasonVerificationUnknown
	case CompletionCleanupFailed:
		return events.DeployErrorCodePruneFailed
	case CompletionProcessInterrupted:
		return events.DeployInterruptReasonProcessInterrupted
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

func (completion Completion) rolloutObligations() deploymentmodel.Verification {
	// A failed pre-apply check has not created a new failed service generation.
	// Keep inherited obligations, but do not require replacement of healthy tasks.
	if !completion.Verification.Known &&
		(completion.Outcome == CompletionInitJobFailed || completion.Outcome == CompletionPolicyRejected) {
		return deploymentmodel.Verification{Known: true}
	}
	return completion.Verification
}
