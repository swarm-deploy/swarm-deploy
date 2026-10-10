package deployment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
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

// StartIfChanged commits running before the caller begins external apply.
// legacyChanged is used only when no verifiable successful baseline exists.
// A nil attempt means no effective change and therefore no deployment was created.
func (s *Service) StartIfChanged(
	ctx context.Context, stack, commit string, file compose.File, legacyChanged bool,
) (*Deployment, error) {
	desired, err := Prepare(file)
	if err != nil {
		return nil, err
	}
	var attempt *Deployment
	err = s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		previous, readErr := s.store.Baseline(ctx, stack)
		start, decisionErr := s.shouldStart(ctx, stack, previous, desired, readErr, legacyChanged)
		if decisionErr != nil || !start {
			return decisionErr
		}
		d := Deployment{ID: uuid.NewString(), Stack: stack, Commit: commit, Status: Running,
			StartedAt: s.now().UTC(), Changes: Compare(previous, desired)}
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

func (s *Service) shouldStart(
	ctx context.Context,
	stack string,
	baseline, desired Prepared,
	baselineErr error,
	legacyChanged bool,
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
	if legacyChanged {
		return true, nil
	}
	var retry bool
	err := s.db.Get(ctx).QueryRowContext(ctx, `SELECT EXISTS(
    SELECT 1 FROM deployments WHERE stack=? AND status IN ('failed','interrupted'))`, stack).Scan(&retry)
	return retry, err
}

// Succeed commits success, the successful baseline, runtime state and source event in T1.
// A commit failure leaves running; callers must not reinterpret it as an apply failure.
func (s *Service) Succeed(ctx context.Context, id string, state model.Stack) error {
	return s.complete(ctx, id, state, Succeeded, "")
}

// Fail records an apply failure without changing the successful desired baseline.
// code must be a stable category selected by the caller, not raw error text.
func (s *Service) Fail(ctx context.Context, id string, state model.Stack, code string) error {
	switch code {
	case "policy_rejected", "init_failed", "apply_failed", "prune_failed":
	default:
		return fmt.Errorf("unsupported deployment failure code")
	}
	state.LastError = code
	return s.complete(ctx, id, state, Failed, code)
}

func (s *Service) complete(ctx context.Context, id string, state model.Stack, status Status, code string) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
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
		d.Status, d.FinishedAt, d.ErrorCode = status, &now, code
		if err = s.store.finish(ctx, d); err != nil {
			return err
		}
		if status == Succeeded {
			if err = s.store.saveBaseline(ctx, id); err != nil {
				return err
			}
		}
		if err = s.runtime.Update(ctx, func(runtime *model.Runtime) { runtime.Stacks[d.Stack] = state }); err != nil {
			return err
		}
		meta := events.DeployEvent{DeploymentID: d.ID, StackName: d.Stack, Commit: d.Commit,
			Services: desired.Compose.Services, StackDefinition: desired}
		if status == Succeeded {
			return s.events.Publish(ctx, &events.DeploySuccess{DeployEvent: meta})
		}
		return s.events.Publish(ctx, &events.DeployFailed{DeployEvent: meta, Error: errors.New(code)})
	})
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
		query, args := "SELECT payload FROM deployments WHERE status='running'", []any{}
		if stack != "" {
			query += " AND stack=?"
			args = append(args, stack)
		}
		running, err := storage.QueryJSON[Deployment](ctx, s.db.Get, query, args...)
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

func (s *Service) interruptOne(ctx context.Context, d Deployment, now time.Time) error {
	desired, err := s.store.Desired(ctx, d.ID)
	if err != nil {
		return err
	}
	d.Status, d.FinishedAt, d.ErrorCode = Interrupted, &now, "process_interrupted"
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
