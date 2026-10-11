package deployment

import (
	"context"
	"encoding/json"
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

// Service commits deployment facts, runtime state and publication atomically.
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

// Inspect reads one deployment in a consistent transaction for lifecycle conflict resolution.
func (s *Service) Inspect(ctx context.Context, id string) (Deployment, error) {
	var result Deployment
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.store.Get(ctx, id)
		return err
	})
	return result, err
}

// StartIfChanged commits running before the caller begins external apply.
// sourceChanged is used only when no verifiable successful baseline exists.
// A nil attempt means no effective change and therefore no deployment was created.
func (s *Service) StartIfChanged(
	ctx context.Context, stack, commit string, file compose.File, sourceChanged bool,
) (*Deployment, error) {
	desired, err := Prepare(file)
	if err != nil {
		return nil, err
	}
	var attempt *Deployment
	err = s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		previous, readErr := s.store.Baseline(ctx, stack)
		start, decisionErr := s.shouldStart(ctx, stack, previous, desired, readErr, sourceChanged)
		if decisionErr != nil || !start {
			return decisionErr
		}
		basis, basisKind, comparisonStatus, basisID, basisErr := s.comparisonBasis(ctx, stack, previous, readErr)
		if basisErr != nil {
			return basisErr
		}
		changes := Compare(basis, desired)
		summary, resources := summarize(changes)
		d := Deployment{ID: uuid.NewString(), Stack: stack, Commit: commit, Status: Running,
			StartedAt: s.now().UTC(), Phase: PhaseApply, ApplyStatus: StageRunning,
			VerificationStatus: StagePending, CleanupStatus: StagePending, ActualStateStatus: ActualStateUnknown,
			ComparisonBasis: basisKind, ComparisonStatus: comparisonStatus, BasisDeploymentID: basisID,
			Summary: summary, Resources: resources, Changes: changes, DiffAvailable: true}
		if insertErr := s.store.insert(ctx, d, desired); insertErr != nil {
			return insertErr
		}
		attempt = &d
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attempt, nil
}

func (s *Service) comparisonBasis(
	ctx context.Context, stack string, baseline Prepared, baselineErr error,
) (Prepared, ComparisonBasis, ComparisonStatus, string, error) {
	last, id, found, err := s.store.latestUnsuccessfulBasis(ctx, stack)
	if err != nil {
		return Prepared{}, "", "", "", err
	}
	if found {
		return last, BasisLastAttempt, ComparisonUnknown, id, nil
	}
	if baselineErr == nil {
		id, err = s.store.baselineDeploymentID(ctx, stack)
		return baseline, BasisSuccessfulBaseline, ComparisonUnknown, id, err
	}
	if errors.Is(baselineErr, ErrNotFound) {
		return Prepared{}, BasisNone, ComparisonUnknown, "", nil
	}
	return Prepared{}, "", "", "", baselineErr
}

func (s *Service) shouldStart(
	ctx context.Context,
	stack string,
	baseline, desired Prepared,
	baselineErr error,
	sourceChanged bool,
) (bool, error) {
	if baselineErr != nil && !errors.Is(baselineErr, ErrNotFound) {
		return false, baselineErr
	}
	if baselineErr == nil {
		if baseline.Digest != desired.Digest {
			return true, nil
		}
		return s.store.hasUnsuccessfulAfterBaseline(ctx, stack)
	}
	if sourceChanged {
		return true, nil
	}
	var retry bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM deployments WHERE stack=? AND status IN ('failed','interrupted'))`, stack).Scan(&retry)
	return retry, err
}

// Succeed commits success, the successful baseline, runtime state and source event in T1.
// A commit failure leaves running; callers must not reinterpret it as an apply failure.
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
// code must be a stable category selected by the caller, not raw error text.
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
		diagnostic: faults.SafeDiagnostic(cause), phase: PhaseApply,
		apply: StageFailed, verification: StageSkipped, cleanup: StageSkipped, actual: ActualStateUnknown})
}

// InterruptVerification records that apply returned but live state could not be observed.
func (s *Service) InterruptVerification(ctx context.Context, id string, state model.Stack) error {
	return s.InterruptVerificationWithError(ctx, id, state, nil)
}

// InterruptVerificationWithError records an unknown live-state outcome and safe diagnostic.
func (s *Service) InterruptVerificationWithError(ctx context.Context, id string, state model.Stack, cause error) error {
	return s.complete(ctx, id, state, completion{status: Interrupted, code: "verification_unknown",
		diagnostic: faults.SafeDiagnostic(cause),
		phase:      PhaseVerification, apply: StageSucceeded, verification: StageUnknown,
		cleanup: StageSkipped, actual: ActualStateUnknown})
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
		diagnostic: faults.SafeDiagnostic(cause), phase: PhaseCleanup,
		apply: StageSucceeded, verification: StageSkipped, cleanup: StageFailed,
		actual: ActualStateObserved, observedAt: &observedAt})
}

type completion struct {
	status       Status
	code         string
	diagnostic   string
	phase        Phase
	apply        StageStatus
	verification StageStatus
	cleanup      StageStatus
	actual       ActualStateStatus
	observedAt   *time.Time
}

func (s *Service) complete(ctx context.Context, id string, state model.Stack, outcome completion) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		state.LastError = outcome.code
		d, err := s.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if d.Status != Running {
			return ErrNotRunning
		}
		desired, err := s.store.Desired(ctx, id)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		d.Status, d.FinishedAt, d.ErrorCode = outcome.status, &now, outcome.code
		d.ErrorDiagnostic = outcome.diagnostic
		d.Phase, d.ApplyStatus = outcome.phase, outcome.apply
		d.VerificationStatus, d.CleanupStatus = outcome.verification, outcome.cleanup
		d.ActualStateStatus, d.ObservedAt = outcome.actual, outcome.observedAt
		if err = s.store.finish(ctx, d); err != nil {
			return err
		}
		if outcome.status == Succeeded {
			if err = s.store.saveBaseline(ctx, id); err != nil {
				return err
			}
		}
		if err = s.runtime.Update(ctx, func(runtime *model.Runtime) { runtime.Stacks[d.Stack] = state }); err != nil {
			return err
		}
		meta := events.DeployEvent{DeploymentID: d.ID, StackName: d.Stack, Commit: d.Commit,
			Services: desired.Compose.Services, StackDefinition: desired}
		if outcome.status == Succeeded {
			return s.events.Publish(ctx, &events.DeploySuccess{DeployEvent: meta})
		}
		if outcome.status == Interrupted {
			return s.events.Publish(ctx, &events.DeployInterrupted{DeploymentID: d.ID, StackName: d.Stack,
				Commit: d.Commit, Services: desired.Compose.Services, Reason: outcome.code,
				Stage: string(outcome.phase), Error: diagnosticError(outcome.diagnostic)})
		}
		return s.events.Publish(ctx, &events.DeployFailed{DeployEvent: meta, Error: diagnosticError(outcome.diagnostic),
			ErrorCode: outcome.code, Stage: string(outcome.phase)})
	})
}

func diagnosticError(diagnostic string) error {
	if diagnostic == "" {
		return nil
	}
	return errors.New(diagnostic)
}

// InterruptRunning recovers incomplete attempts at startup without inventing success.
// It must run before the controller starts, on a database with a single active instance.
func (s *Service) InterruptRunning(ctx context.Context) error {
	return s.interrupt(ctx, "")
}

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
		now := s.now().UTC()
		for _, d := range running {
			if err = s.interruptOne(ctx, d, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) running(ctx context.Context, stack string) ([]Deployment, error) {
	dataset := goqu.Dialect("sqlite3").From("deployments").Prepared(true).
		Select("payload").Where(goqu.Ex{"status": Running})
	if stack != "" {
		dataset = dataset.Where(goqu.Ex{"stack": stack})
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	running := []Deployment{}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		var deployment Deployment
		if err = json.Unmarshal(payload, &deployment); err != nil {
			return nil, err
		}
		running = append(running, deployment)
	}
	return running, rows.Err()
}

func (s *Service) interruptOne(ctx context.Context, d Deployment, now time.Time) error {
	desired, err := s.store.Desired(ctx, d.ID)
	if err != nil {
		return err
	}
	d.Status, d.FinishedAt, d.ErrorCode = Interrupted, &now, "process_interrupted"
	d.Phase, d.ApplyStatus = PhaseApply, StageUnknown
	d.VerificationStatus, d.CleanupStatus = StageSkipped, StageSkipped
	d.ActualStateStatus, d.ObservedAt = ActualStateUnknown, nil
	if err = s.store.finish(ctx, d); err != nil {
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
		DeploymentID: d.ID, StackName: d.Stack, Commit: d.Commit,
		Services: desired.Compose.Services, Reason: "process_interrupted",
	})
}
