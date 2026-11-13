package syncpause

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mocks.go -package=syncpause

// StackProvider supplies the currently accepted GitOps stack identities.
type StackProvider interface {
	// Stacks returns configured stack definitions.
	Stacks() []config.StackSpec
}

// ErrStackNotManaged reports a target absent from accepted GitOps configuration and runtime state.
var ErrStackNotManaged = errors.New("stack is not managed by GitOps")

// ErrExpirationNotFuture reports an expiration which cannot create an active pause.
var ErrExpirationNotFuture = errors.New("pause expiration must be in the future")

// Service owns pause validation, persistence, effective-state calculation and events.
type Service struct {
	db     storage.Database
	repo   *Repository
	state  modelstore.ReadStore
	stacks StackProvider
	events dispatcher.Dispatcher
	now    func() time.Time
}

// NewService constructs the GitOps pause domain service.
func NewService(
	db storage.Database,
	state modelstore.ReadStore,
	stacks StackProvider,
	eventDispatcher dispatcher.Dispatcher,
) *Service {
	return &Service{db: db, repo: NewRepository(db), state: state, stacks: stacks, events: eventDispatcher, now: time.Now}
}

// Active returns the current stack pause policy without persisting expiration transitions.
func (s *Service) Active(ctx context.Context, stack string) (ActivePolicy, error) {
	return s.repo.Active(ctx, stack, s.now())
}

// GetStack returns the active pause and calculated effectively paused service names.
func (s *Service) GetStack(ctx context.Context, stack string) (StackState, error) {
	runtime, err := s.state.Read(ctx)
	if err != nil {
		return StackState{}, err
	}
	policy, err := s.repo.Active(ctx, stack, s.now())
	if err != nil {
		return StackState{}, err
	}
	stackState, tracked := runtime.Stacks[stack]
	if !tracked && !s.stackConfigured(stack) && policy.StackPause == nil {
		return StackState{}, ErrStackNotManaged
	}
	managed := make([]string, 0, len(stackState.Services))
	for name := range stackState.Services {
		managed = append(managed, name)
	}
	sort.Strings(managed)
	effective := []string(nil)
	if policy.Preserves() {
		effective = slices.Clone(managed)
	}
	return StackState{
		StackPause: policy.StackPause, ManagedServices: managed, EffectivelyPausedServices: effective,
	}, nil
}

// SetStack creates or changes a stack pause.
func (s *Service) SetStack(ctx context.Context, stack string, expiresAt *time.Time) (bool, error) {
	if !s.stackConfigured(stack) {
		return false, ErrStackNotManaged
	}
	pause := Pause{StackName: stack, ExpiresAt: normalizedExpiration(expiresAt)}
	if pause.ExpiresAt != nil && !pause.ExpiresAt.After(s.now()) {
		return false, ErrExpirationNotFuture
	}
	changed := false
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		changed, err = s.repo.Upsert(ctx, pause)
		if err != nil || !changed {
			return err
		}
		return s.events.Publish(ctx, &events.SyncPaused{SyncPauseEvent: events.SyncPauseEvent{
			StackName: pause.StackName, ExpiresAt: pause.ExpiresAt,
		}})
	})
	return changed, err
}

// ResumeStack removes the stack pause.
func (s *Service) ResumeStack(ctx context.Context, stack string) (bool, error) {
	removed := false
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		removed, err = s.repo.Delete(ctx, stack)
		if err != nil || !removed {
			return err
		}
		return s.events.Publish(ctx, &events.SyncResumed{SyncPauseEvent: events.SyncPauseEvent{StackName: stack}})
	})
	return removed, err
}

// Expire removes elapsed pauses and publishes one event per row in the same transaction.
func (s *Service) Expire(ctx context.Context) ([]Pause, error) {
	now := s.now().UTC()
	var expired []Pause
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		expired, err = s.repo.Expired(ctx, now)
		if err != nil {
			return err
		}
		for _, pause := range expired {
			removed, deleteErr := s.repo.Delete(ctx, pause.StackName)
			if deleteErr != nil {
				return deleteErr
			}
			if !removed {
				return fmt.Errorf("expired pause disappeared during transaction: %s", pause.StackName)
			}
			if err = s.events.Publish(ctx, &events.SyncPauseExpired{SyncPauseEvent: events.SyncPauseEvent{
				StackName: pause.StackName, ExpiresAt: pause.ExpiresAt,
			}}); err != nil {
				return err
			}
		}
		return nil
	})
	return expired, err
}

func (s *Service) stackConfigured(name string) bool {
	return slices.ContainsFunc(s.stacks.Stacks(), func(stack config.StackSpec) bool { return stack.Name == name })
}

func normalizedExpiration(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

// PreserveNames returns every desired and prior service name while the stack is paused.
func PreserveNames(policy ActivePolicy, desired []string, prior gitopsmodel.Stack) []string {
	if !policy.Preserves() {
		return nil
	}
	names := make(map[string]struct{}, len(desired)+len(prior.Services))
	for _, name := range desired {
		names[name] = struct{}{}
	}
	for name := range prior.Services {
		names[name] = struct{}{}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
