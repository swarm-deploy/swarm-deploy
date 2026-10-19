package deployment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// Publisher only persists domain facts; subscribers run after commit.
type Publisher interface {
	// Publish joins the transaction carried in context.
	Publish(context.Context, events.Event) error
}

// Service commits deployment facts, attempts, runtime state and publication atomically.
// It never invokes Docker or another module's business service.
type Service struct {
	db      *storage.Database
	store   *Store
	runtime *modelstore.SQLStore
	events  Publisher
	now     func() time.Time
}

// NewService creates the deployment boundary using the shared context transactor.
func NewService(db *storage.Database, publisher Publisher) *Service {
	return &Service{db: db, store: NewStore(db), runtime: modelstore.NewSQLStore(db), events: publisher, now: time.Now}
}

// Inspect reads one attempt in a consistent transaction for lifecycle conflict resolution.
func (s *Service) Inspect(ctx context.Context, id string) (Attempt, error) {
	var result Attempt
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.store.getAttempt(ctx, id)
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

// StartIfChanged creates an attempt before the caller begins external apply.
// Unchanged successful or currently running desired state returns nil. Failed and
// interrupted state receives a new attempt on the existing Deployment.
func (s *Service) StartIfChanged(
	ctx context.Context, stack, commit string, file compose.File, sourceChanged bool,
) (*Attempt, error) {
	desired, err := Prepare(file)
	if err != nil {
		return nil, err
	}
	var result *Attempt
	err = s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var startErr error
		result, startErr = s.startPrepared(ctx, stack, commit, desired, sourceChanged)
		return startErr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) startPrepared(
	ctx context.Context, stack, commit string, desired Prepared, sourceChanged bool,
) (*Attempt, error) {
	latest, latestDesired, err := s.store.latest(ctx, stack)
	if errors.Is(err, ErrNotFound) {
		if !sourceChanged {
			return nil, nil //nolint:nilnil // Nil attempt explicitly means no deployment execution.
		}
		return s.createDeployment(ctx, stack, commit, desired)
	}
	if err != nil {
		return nil, err
	}
	if latestDesired.Digest == desired.Digest {
		return s.startExisting(ctx, latest)
	}
	if err = s.supersedePrevious(ctx, latest); err != nil {
		return nil, err
	}
	return s.createDeployment(ctx, stack, commit, desired)
}

func (s *Service) startExisting(ctx context.Context, latest Deployment) (*Attempt, error) {
	switch latest.Status {
	case Succeeded, Running:
		return nil, nil //nolint:nilnil // Nil attempt explicitly means no deployment execution.
	case Failed, Interrupted:
		attempt := newAttempt(latest.ID, latest.AttemptCount+1, s.now())
		if err := s.store.insertAttempt(ctx, latest, attempt); err != nil {
			return nil, err
		}
		return &attempt, nil
	case Superseded:
		return nil, ErrInconsistentDeployment
	default:
		return nil, fmt.Errorf("unsupported deployment status %q", latest.Status)
	}
}

func (s *Service) supersedePrevious(ctx context.Context, latest Deployment) error {
	if latest.Status != Running {
		return nil
	}
	attempt, err := s.runningAttempt(ctx, latest)
	if err != nil {
		return err
	}
	return s.interruptOne(ctx, attempt, s.now().UTC())
}

func (s *Service) runningAttempt(ctx context.Context, deployment Deployment) (Attempt, error) {
	if len(deployment.Attempts) > 0 {
		return deployment.Attempts[0], nil
	}
	return s.materializeLegacyRunning(ctx, deployment)
}

func (s *Service) createDeployment(
	ctx context.Context, stack, commit string, desired Prepared,
) (*Attempt, error) {
	d, attempt, err := s.newDeployment(ctx, stack, commit, desired)
	if err != nil {
		return nil, err
	}
	if err = s.store.insert(ctx, d, desired, attempt); err != nil {
		return nil, err
	}
	return &attempt, nil
}

func (s *Service) newDeployment(
	ctx context.Context, stack, commit string, desired Prepared,
) (Deployment, Attempt, error) {
	basis, basisKind, basisID, err := s.comparisonBasis(ctx, stack)
	if err != nil {
		return Deployment{}, Attempt{}, err
	}
	changes := Compare(basis, desired)
	summary, resources := summarize(changes)
	id := uuid.NewString()
	attempt := newAttempt(id, 1, s.now())
	d := Deployment{
		ID: id, Stack: stack, Commit: commit, Status: Running, StartedAt: attempt.StartedAt,
		Phase: PhaseApply, ApplyStatus: StageRunning, VerificationStatus: StagePending,
		CleanupStatus: StagePending, ActualStateStatus: ActualStateUnknown,
		ComparisonBasis: basisKind, ComparisonStatus: ComparisonUnknown, BasisDeploymentID: basisID,
		Summary: summary, Resources: resources, Changes: changes, DiffAvailable: true,
		LatestAttemptID: attempt.ID, AttemptCount: 1,
	}
	return d, attempt, nil
}

func newAttempt(deploymentID string, number int, now time.Time) Attempt {
	return Attempt{
		ID: uuid.NewString(), DeploymentID: deploymentID, Number: number, Status: Running,
		StartedAt: now.UTC(), Phase: PhaseApply, ApplyStatus: StageRunning,
		VerificationStatus: StagePending, CleanupStatus: StagePending, ActualStateStatus: ActualStateUnknown,
	}
}

func (s *Service) comparisonBasis(ctx context.Context, stack string) (Prepared, ComparisonBasis, string, error) {
	baseline, err := s.store.Baseline(ctx, stack)
	if errors.Is(err, ErrNotFound) {
		return Prepared{}, BasisNone, "", nil
	}
	if err != nil {
		return Prepared{}, "", "", err
	}
	id, err := s.store.baselineDeploymentID(ctx, stack)
	return baseline, BasisSuccessfulBaseline, id, err
}

// Succeed commits success, the successful baseline, runtime state and source event atomically.
func (s *Service) Succeed(ctx context.Context, id string, state model.Stack) error {
	return s.complete(ctx, id, state, completion{status: Succeeded, phase: PhaseCompleted,
		apply: StageSucceeded, verification: StageSkipped, cleanup: StageSucceeded, actual: ActualStateUnknown})
}

// SucceedObserved records a successful apply and a read of live services, not convergence.
func (s *Service) SucceedObserved(ctx context.Context, id string, state model.Stack, observedAt time.Time) error {
	return s.complete(ctx, id, state, completion{status: Succeeded, phase: PhaseCompleted,
		apply: StageSucceeded, verification: StageSkipped, cleanup: StageSucceeded,
		actual: ActualStateObserved, observedAt: &observedAt})
}

// Fail records an apply failure without changing the successful desired baseline.
func (s *Service) Fail(ctx context.Context, id string, state model.Stack, code string) error {
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
func (s *Service) FailApply(ctx context.Context, id string, state model.Stack, code string) error {
	return s.FailApplyWithError(ctx, id, state, code, nil)
}

// FailApplyWithError records a known Docker apply failure and a safe diagnostic.
func (s *Service) FailApplyWithError(
	ctx context.Context, id string, state model.Stack, code string, cause error,
) error {
	switch code {
	case "policy_rejected", "init_failed", "apply_failed":
	default:
		return fmt.Errorf("unsupported apply failure code")
	}
	return s.complete(ctx, id, state, completion{status: Failed, code: code,
		cause: faults.SafeError(cause), phase: PhaseApply, apply: StageFailed,
		verification: StageSkipped, cleanup: StageSkipped, actual: ActualStateUnknown})
}

// InterruptVerification records that apply returned but live state could not be observed.
func (s *Service) InterruptVerification(ctx context.Context, id string, state model.Stack) error {
	return s.InterruptVerificationWithError(ctx, id, state, nil)
}

// InterruptVerificationWithError records an unknown live-state outcome and safe diagnostic.
func (s *Service) InterruptVerificationWithError(ctx context.Context, id string, state model.Stack, cause error) error {
	return s.complete(ctx, id, state, completion{status: Interrupted, code: "verification_unknown",
		cause: faults.SafeError(cause), phase: PhaseVerification, apply: StageSucceeded,
		verification: StageUnknown, cleanup: StageSkipped, actual: ActualStateUnknown})
}

// FailCleanup records a cleanup failure after successful apply and live-state observation.
func (s *Service) FailCleanup(
	ctx context.Context, id string, state model.Stack, code string, observedAt time.Time,
) error {
	return s.FailCleanupWithError(ctx, id, state, code, observedAt, nil)
}

// FailCleanupWithError records cleanup failure and safe diagnostic context.
func (s *Service) FailCleanupWithError(
	ctx context.Context, id string, state model.Stack, code string, observedAt time.Time, cause error,
) error {
	if code != "prune_failed" {
		return fmt.Errorf("unsupported cleanup failure code")
	}
	return s.complete(ctx, id, state, completion{status: Failed, code: code,
		cause: faults.SafeError(cause), phase: PhaseCleanup, apply: StageSucceeded,
		verification: StageSkipped, cleanup: StageFailed, actual: ActualStateObserved, observedAt: &observedAt})
}

type completion struct {
	status       Status
	code         string
	cause        error
	phase        Phase
	apply        StageStatus
	verification StageStatus
	cleanup      StageStatus
	actual       ActualStateStatus
	observedAt   *time.Time
}

func (s *Service) complete(ctx context.Context, id string, state model.Stack, outcome completion) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		return s.completeTransaction(ctx, id, state, outcome)
	})
}

func (s *Service) completeTransaction(ctx context.Context, id string, state model.Stack, outcome completion) error {
	state.LastError = outcome.code
	attempt, err := s.store.getAttempt(ctx, id)
	if err != nil {
		return err
	}
	d, err := s.store.Get(ctx, attempt.DeploymentID)
	if err != nil {
		return err
	}
	if attempt.Status != Running || d.Status != Running || d.LatestAttemptID != attempt.ID {
		return ErrNotRunning
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
	if err = s.store.finish(ctx, d, attempt); err != nil {
		return err
	}
	if outcome.status == Succeeded {
		if err = s.store.saveBaseline(ctx, d.ID); err != nil {
			return err
		}
	}
	if err = s.runtime.Update(ctx, func(runtime *model.Runtime) { runtime.Stacks[d.Stack] = state }); err != nil {
		return err
	}
	return s.publishCompletion(ctx, d, attempt, desired, outcome)
}

func (s *Service) publishCompletion(
	ctx context.Context, d Deployment, attempt Attempt, desired compose.File, outcome completion,
) error {
	meta := events.DeployEvent{DeploymentID: d.ID, AttemptID: attempt.ID, StackName: d.Stack, Commit: d.Commit,
		Services: desired.Compose.Services, StackDefinition: desired}
	switch outcome.status {
	case Succeeded:
		return s.events.Publish(ctx, &events.DeploySuccess{DeployEvent: meta})
	case Interrupted:
		return s.events.Publish(ctx, &events.DeployInterrupted{DeploymentID: d.ID, AttemptID: attempt.ID,
			StackName: d.Stack, Commit: d.Commit, Services: desired.Compose.Services, Reason: outcome.code,
			Stage: string(outcome.phase), Error: outcome.cause})
	case Failed:
		return s.events.Publish(ctx, &events.DeployFailed{DeployEvent: meta, Error: outcome.cause,
			ErrorCode: outcome.code, Stage: string(outcome.phase)})
	case Running, Superseded:
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
		running, err := s.running(ctx, stack)
		if err != nil {
			return err
		}
		legacy, err := s.store.legacyRunning(ctx, stack)
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
			if err = s.interruptOne(ctx, attempt, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) materializeLegacyRunning(ctx context.Context, deployment Deployment) (Attempt, error) {
	attempt := newAttempt(deployment.ID, 1, deployment.StartedAt)
	if err := s.store.insertAttemptRow(ctx, attempt); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

func (s *Service) running(ctx context.Context, stack string) ([]Attempt, error) {
	dataset := goqu.Dialect("sqlite3").From(goqu.T("deployment_attempts").As("attempt")).Prepared(true).
		Join(goqu.T("deployments").As("deployment"),
			goqu.On(goqu.I("deployment.id").Eq(goqu.I("attempt.deployment_id")))).
		Select(
			goqu.I("attempt.id"), goqu.I("attempt.deployment_id"), goqu.I("attempt.number"),
			goqu.I("attempt.status"), goqu.I("attempt.error_code"), goqu.I("attempt.phase"),
			goqu.I("attempt.started_at"), goqu.I("attempt.finished_at"), goqu.I("attempt.details_payload"),
		).Where(goqu.Ex{"attempt.status": Running})
	if stack != "" {
		dataset = dataset.Where(goqu.Ex{"deployment.stack": stack})
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []attemptRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	result := make([]Attempt, 0, len(rows))
	for _, row := range rows {
		attempt, convertErr := row.attempt()
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, attempt)
	}
	return result, nil
}

func (s *Service) interruptOne(ctx context.Context, attempt Attempt, now time.Time) error {
	d, err := s.store.Get(ctx, attempt.DeploymentID)
	if err != nil {
		return err
	}
	desired, err := s.store.Desired(ctx, d.ID)
	if err != nil {
		return err
	}
	attempt.Status, attempt.FinishedAt, attempt.ErrorCode = Interrupted, &now, "process_interrupted"
	attempt.Phase, attempt.ApplyStatus = PhaseApply, StageUnknown
	attempt.VerificationStatus, attempt.CleanupStatus = StageSkipped, StageSkipped
	attempt.ActualStateStatus, attempt.ObservedAt = ActualStateUnknown, nil
	if err = s.store.finish(ctx, d, attempt); err != nil {
		return err
	}
	if err = s.runtime.Update(ctx, func(runtime *model.Runtime) {
		state := runtime.Stacks[d.Stack]
		state.LastCommit, state.LastError = d.Commit, "process_interrupted"
		if state.Services == nil {
			state.Services = map[string]model.Service{}
		}
		runtime.Stacks[d.Stack] = state
	}); err != nil {
		return err
	}
	return s.events.Publish(ctx, &events.DeployInterrupted{
		DeploymentID: d.ID, AttemptID: attempt.ID, StackName: d.Stack, Commit: d.Commit,
		Services: desired.Compose.Services, Reason: "process_interrupted",
	})
}
