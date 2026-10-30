package deployment

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// ErrInconsistentDeployment aliases the canonical Deployments module error for compatibility.
var ErrInconsistentDeployment = deployments.ErrInconsistentDeployment

// Publisher only persists domain facts; subscribers run after commit.
type Publisher interface {
	// Publish joins the transaction carried in context.
	Publish(context.Context, events.Event) error
}

// Service selects deployment outcomes, orchestrates recovery, and projects GitOps runtime state.
type Service struct {
	db          storage.Database
	store       *deploymentstore.Store
	persistence *deployments.Service
	runtime     *modelstore.SQLStore
	now         func() time.Time
}

// NewService creates the deployment boundary using the shared context transactor.
func NewService(db storage.Database, publisher Publisher) *Service {
	service := &Service{
		db: db, store: deploymentstore.NewStore(db), runtime: modelstore.NewSQLStore(db), now: time.Now,
	}
	service.persistence = deployments.NewService(db, publisher, deployments.WithClock(func() time.Time {
		return service.now()
	}))
	return service
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
	return s.complete(ctx, id, state, deployments.Completion{
		Outcome: deployments.CompletionSucceeded,
	})
}

// SucceedObserved records a successful apply and a read of live services, not convergence.
func (s *Service) SucceedObserved(ctx context.Context, id string, state gitopsmodel.Stack, observedAt time.Time) error {
	return s.complete(ctx, id, state, deployments.Completion{
		Outcome: deployments.CompletionSucceeded, ObservedAt: &observedAt,
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
	var outcome deployments.CompletionOutcome
	switch code {
	case "policy_rejected":
		outcome = deployments.CompletionPolicyRejected
	case "init_failed":
		outcome = deployments.CompletionInitJobFailed
	case "apply_failed":
		outcome = deployments.CompletionApplyFailed
	default:
		return fmt.Errorf("unsupported apply failure code")
	}
	return s.complete(ctx, id, state, deployments.Completion{
		Outcome: outcome, Diagnostic: cause,
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
	return s.complete(ctx, id, state, deployments.Completion{
		Outcome: deployments.CompletionVerificationInterrupted, Diagnostic: cause,
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
	return s.complete(ctx, id, state, deployments.Completion{
		Outcome: deployments.CompletionCleanupFailed, Diagnostic: cause, ObservedAt: &observedAt,
	})
}

func (s *Service) complete(
	ctx context.Context,
	id string,
	state gitopsmodel.Stack,
	completion deployments.Completion,
) error {
	state.LastError = completion.ErrorCode()
	project := func(ctx context.Context, deployment deploymentmodel.Deployment) error {
		return s.runtime.Update(ctx, func(runtime *gitopsmodel.Runtime) {
			runtime.Stacks[deployment.Stack] = state
		})
	}
	return s.persistence.Complete(ctx, id, completion, project)
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
		for _, attempt := range running {
			synchronized, syncErr := s.store.SynchronizeRunningAttempt(ctx, attempt)
			if syncErr != nil {
				return syncErr
			}
			if synchronized {
				continue
			}
			if err = s.interruptOne(ctx, attempt); err != nil {
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

func (s *Service) interruptOne(ctx context.Context, attempt deploymentmodel.Attempt) error {
	return s.persistence.Complete(ctx, attempt.ID, deployments.Completion{
		Outcome: deployments.CompletionProcessInterrupted,
	}, s.ProjectInterruption)
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
