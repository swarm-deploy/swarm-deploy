package deployment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// ErrInconsistentDeployment means controller state disagrees with the persisted deployment.
var ErrInconsistentDeployment = errors.New("deployment row and payload are inconsistent")

// Publisher only persists domain facts; subscribers run after commit.
type Publisher interface {
	// Publish joins the transaction carried in context.
	Publish(context.Context, events.Event) error
}

// Service commits deployment completion, recovery, runtime state and publication atomically.
// It never invokes Docker or another module's business service.
type Service struct {
	db          storage.Database
	store       *deploymentstore.Store
	persistence *deployments.Service
	runtime     *modelstore.SQLStore
	events      Publisher
	now         func() time.Time
}

// NewService creates the deployment boundary using the shared context transactor.
func NewService(db storage.Database, publisher Publisher) *Service {
	return &Service{
		db: db, store: deploymentstore.NewStore(db), persistence: deployments.NewService(db, publisher),
		runtime: modelstore.NewSQLStore(db), events: publisher, now: time.Now,
	}
}

// SaveInitJobExecution stores observational Init Job history outside lifecycle completion transactions.
func (s *Service) SaveInitJobExecution(
	ctx context.Context,
	execution deploymentmodel.InitJobExecution,
) error {
	return s.store.SaveInitJobExecution(ctx, execution)
}

// Inspect reads one attempt in a consistent transaction for lifecycle conflict resolution.
func (s *Service) Inspect(ctx context.Context, id string) (deploymentmodel.Attempt, error) {
	var result deploymentmodel.Attempt
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.store.GetAttempt(ctx, id)
		if err != nil {
			return err
		}
		deployment, err := s.store.Get(ctx, result.DeploymentID)
		if err == nil {
			result.Stack = deployment.Stack
		}
		return err
	})
	return result, err
}

func newAttempt(deploymentID string, number int, now time.Time) deploymentmodel.Attempt {
	return deploymentmodel.Attempt{
		ID: uuid.NewString(), DeploymentID: deploymentID, Number: number, Status: deploymentmodel.Running,
		StartedAt: now.UTC(), Phase: deploymentmodel.PhaseApply, ApplyStatus: deploymentmodel.StageRunning,
		VerificationStatus: deploymentmodel.StagePending,
		CleanupStatus:      deploymentmodel.StagePending,
		ActualStateStatus:  deploymentmodel.ActualStateUnknown,
	}
}

// Succeed commits success, the successful baseline, runtime state and source event atomically.
func (s *Service) Succeed(ctx context.Context, id string, state gitopsmodel.Stack) error {
	return s.complete(ctx, id, state, completion{
		status: deploymentmodel.Succeeded, phase: deploymentmodel.PhaseCompleted,
		apply: deploymentmodel.StageSucceeded, verification: deploymentmodel.StageSkipped,
		cleanup: deploymentmodel.StageSucceeded, actual: deploymentmodel.ActualStateUnknown,
	})
}

// SucceedObserved records a successful apply and a read of live services, not convergence.
func (s *Service) SucceedObserved(ctx context.Context, id string, state gitopsmodel.Stack, observedAt time.Time) error {
	return s.complete(ctx, id, state, completion{
		status: deploymentmodel.Succeeded, phase: deploymentmodel.PhaseCompleted,
		apply: deploymentmodel.StageSucceeded, verification: deploymentmodel.StageSkipped,
		cleanup: deploymentmodel.StageSucceeded, actual: deploymentmodel.ActualStateObserved,
		observedAt: &observedAt,
	})
}

// Fail records an apply failure without changing the successful desired baseline.
func (s *Service) Fail(ctx context.Context, id string, state gitopsmodel.Stack, code string) error {
	switch code {
	case "policy_rejected", "init_failed", "apply_failed":
		return s.FailApply(ctx, id, state, code)
	case "prune_failed":
		return s.FailCleanup(ctx, id, state, code, s.now().UTC())
	default:
		return fmt.Errorf("unsupported deployment failure code")
	}
}

// FailApply records a known Docker apply failure with unknown actual state.
func (s *Service) FailApply(ctx context.Context, id string, state gitopsmodel.Stack, code string) error {
	return s.FailApplyWithError(ctx, id, state, code, nil)
}

// FailApplyWithError records a known Docker apply failure and a safe diagnostic.
func (s *Service) FailApplyWithError(
	ctx context.Context, id string, state gitopsmodel.Stack, code string, cause error,
) error {
	switch code {
	case "policy_rejected", "init_failed", "apply_failed":
	default:
		return fmt.Errorf("unsupported apply failure code")
	}
	return s.complete(ctx, id, state, completion{
		status: deploymentmodel.Failed, code: code,
		cause: faults.SafeError(cause), phase: deploymentmodel.PhaseApply, apply: deploymentmodel.StageFailed,
		verification: deploymentmodel.StageSkipped, cleanup: deploymentmodel.StageSkipped,
		actual: deploymentmodel.ActualStateUnknown,
	})
}

// InterruptVerification records that apply returned but live state could not be observed.
func (s *Service) InterruptVerification(ctx context.Context, id string, state gitopsmodel.Stack) error {
	return s.InterruptVerificationWithError(ctx, id, state, nil)
}

// InterruptVerificationWithError records an unknown live-state outcome and safe diagnostic.
func (s *Service) InterruptVerificationWithError(
	ctx context.Context, id string, state gitopsmodel.Stack, cause error,
) error {
	return s.complete(ctx, id, state, completion{
		status: deploymentmodel.Interrupted, code: "verification_unknown",
		cause: faults.SafeError(cause), phase: deploymentmodel.PhaseVerification, apply: deploymentmodel.StageSucceeded,
		verification: deploymentmodel.StageUnknown, cleanup: deploymentmodel.StageSkipped,
		actual: deploymentmodel.ActualStateUnknown,
	})
}

// FailCleanup records a cleanup failure after successful apply and live-state observation.
func (s *Service) FailCleanup(
	ctx context.Context, id string, state gitopsmodel.Stack, code string, observedAt time.Time,
) error {
	return s.FailCleanupWithError(ctx, id, state, code, observedAt, nil)
}

// FailCleanupWithError records cleanup failure and safe diagnostic context.
func (s *Service) FailCleanupWithError(
	ctx context.Context, id string, state gitopsmodel.Stack, code string, observedAt time.Time, cause error,
) error {
	if code != "prune_failed" {
		return fmt.Errorf("unsupported cleanup failure code")
	}
	return s.complete(ctx, id, state, completion{
		status: deploymentmodel.Failed, code: code,
		cause: faults.SafeError(cause), phase: deploymentmodel.PhaseCleanup, apply: deploymentmodel.StageSucceeded,
		verification: deploymentmodel.StageSkipped, cleanup: deploymentmodel.StageFailed,
		actual: deploymentmodel.ActualStateObserved, observedAt: &observedAt,
	})
}

type completion struct {
	status       deploymentmodel.Status
	code         string
	cause        error
	phase        deploymentmodel.Phase
	apply        deploymentmodel.StageStatus
	verification deploymentmodel.StageStatus
	cleanup      deploymentmodel.StageStatus
	actual       deploymentmodel.ActualStateStatus
	observedAt   *time.Time
}

func (s *Service) complete(ctx context.Context, id string, state gitopsmodel.Stack, outcome completion) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		return s.completeTransaction(ctx, id, state, outcome)
	})
}

func (s *Service) completeTransaction(
	ctx context.Context, id string, state gitopsmodel.Stack, outcome completion,
) error {
	state.LastError = outcome.code
	attempt, err := s.store.GetAttempt(ctx, id)
	if err != nil {
		return err
	}
	d, err := s.store.Get(ctx, attempt.DeploymentID)
	if err != nil {
		return err
	}
	if attempt.Status != deploymentmodel.Running ||
		d.Status != deploymentmodel.Running ||
		d.LatestAttemptID != attempt.ID {
		return deploymentstore.ErrNotRunning
	}
	desired, err := s.store.Desired(ctx, d.ID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	attempt.Status, attempt.FinishedAt, attempt.ErrorCode = outcome.status, &now, outcome.code
	attempt.ErrorDiagnostic = faults.SafeDiagnostic(outcome.cause)
	attempt.Phase, attempt.ApplyStatus = outcome.phase, outcome.apply
	attempt.VerificationStatus, attempt.CleanupStatus = outcome.verification, outcome.cleanup
	attempt.ActualStateStatus, attempt.ObservedAt = outcome.actual, outcome.observedAt
	if err = s.store.FinishAttempt(ctx, d, attempt); err != nil {
		return err
	}
	if outcome.status == deploymentmodel.Succeeded {
		if err = s.store.SaveBaseline(ctx, d.ID); err != nil {
			return err
		}
	}
	if err = s.runtime.Update(ctx, func(runtime *gitopsmodel.Runtime) { runtime.Stacks[d.Stack] = state }); err != nil {
		return err
	}
	return s.publishCompletion(ctx, d, attempt, desired, outcome)
}

func (s *Service) publishCompletion(
	ctx context.Context,
	d deploymentmodel.Deployment,
	attempt deploymentmodel.Attempt,
	desired compose.File,
	outcome completion,
) error {
	meta := events.DeployEvent{DeploymentID: d.ID, AttemptID: attempt.ID, StackName: d.Stack, Commit: d.Commit,
		Services: desired.Compose.Services, StackDefinition: desired}
	switch outcome.status {
	case deploymentmodel.Succeeded:
		return s.events.Publish(ctx, &events.DeploySuccess{DeployEvent: meta})
	case deploymentmodel.Interrupted:
		return s.events.Publish(ctx, &events.DeployInterrupted{DeploymentID: d.ID, AttemptID: attempt.ID,
			StackName: d.Stack, Commit: d.Commit, Services: desired.Compose.Services, Reason: outcome.code,
			Stage: string(outcome.phase), Error: outcome.cause})
	case deploymentmodel.Failed:
		return s.events.Publish(ctx, &events.DeployFailed{DeployEvent: meta, Error: outcome.cause,
			ErrorCode: outcome.code, Stage: string(outcome.phase)})
	case deploymentmodel.Running, deploymentmodel.Superseded:
		return fmt.Errorf("unsupported completion status %q", outcome.status)
	default:
		return fmt.Errorf("unsupported completion status %q", outcome.status)
	}
}

// InterruptRunning recovers incomplete attempts at startup without inventing success.
func (s *Service) InterruptRunning(ctx context.Context) error { return s.interrupt(ctx, "") }

// InterruptStack recovers an unknown running attempt before the next serialized reconcile.
func (s *Service) InterruptStack(ctx context.Context, stack string) error {
	return s.interrupt(ctx, stack)
}

func (s *Service) interrupt(ctx context.Context, stack string) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		running, err := s.store.RunningAttempts(ctx, stack)
		if err != nil {
			return err
		}
		legacy, err := s.store.LegacyRunning(ctx, stack)
		if err != nil {
			return err
		}
		for _, deployment := range legacy {
			attempt, materializeErr := s.materializeLegacyRunning(ctx, deployment)
			if materializeErr != nil {
				return materializeErr
			}
			running = append(running, attempt)
		}
		now := s.now().UTC()
		for _, attempt := range running {
			synchronized, syncErr := s.store.SynchronizeRunningAttempt(ctx, attempt)
			if syncErr != nil {
				return syncErr
			}
			if synchronized {
				continue
			}
			if err = s.interruptOne(ctx, attempt, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) materializeLegacyRunning(
	ctx context.Context,
	deployment deploymentmodel.Deployment,
) (deploymentmodel.Attempt, error) {
	attempt := newAttempt(deployment.ID, 1, deployment.StartedAt)
	if err := s.store.InsertLegacyAttempt(ctx, attempt); err != nil {
		return deploymentmodel.Attempt{}, err
	}
	return attempt, nil
}

func (s *Service) interruptOne(ctx context.Context, attempt deploymentmodel.Attempt, now time.Time) error {
	return s.persistence.InterruptAttempt(ctx, attempt, now, s.ProjectInterruption)
}

// ProjectInterruption updates GitOps runtime state for an interrupted deployment.
func (s *Service) ProjectInterruption(ctx context.Context, d deploymentmodel.Deployment) error {
	return s.runtime.Update(ctx, func(runtime *gitopsmodel.Runtime) {
		state := runtime.Stacks[d.Stack]
		state.LastCommit, state.LastError = d.Commit, "process_interrupted"
		if state.Services == nil {
			state.Services = map[string]gitopsmodel.Service{}
		}
		runtime.Stacks[d.Stack] = state
	})
}
