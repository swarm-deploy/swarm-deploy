package syncpause

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
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

// ErrServiceNotManaged reports a target absent from GitOps runtime state.
var ErrServiceNotManaged = errors.New("service is not managed by GitOps")

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

// Active returns current direct pause policy without persisting expiration transitions.
func (s *Service) Active(ctx context.Context, stack string) (ActivePolicy, error) {
	return s.repo.Active(ctx, stack, s.now())
}

// GetStack returns active direct pauses and calculated effective service names.
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
	if !tracked && !s.stackConfigured(stack) && policy.StackPause == nil && len(policy.ServicePauses) == 0 {
		return StackState{}, ErrStackNotManaged
	}
	managed := make([]string, 0, len(stackState.Services))
	for name := range stackState.Services {
		managed = append(managed, name)
	}
	sort.Strings(managed)
	servicePauses := make([]Pause, 0, len(policy.ServicePauses))
	for _, pause := range policy.ServicePauses {
		servicePauses = append(servicePauses, pause)
	}
	sort.Slice(servicePauses, func(i, j int) bool { return servicePauses[i].ServiceName < servicePauses[j].ServiceName })
	effective := make([]string, 0, len(managed))
	for _, name := range managed {
		if policy.Preserves(name) {
			effective = append(effective, name)
		}
	}
	return StackState{
		StackPause: policy.StackPause, ServicePauses: servicePauses,
		ManagedServices: managed, EffectivelyPausedServices: effective,
	}, nil
}

// GetService returns direct and effective pause state for one service.
func (s *Service) GetService(ctx context.Context, stack, service string) (ServiceState, error) {
	if err := s.validateService(ctx, stack, service, true); err != nil {
		return ServiceState{}, err
	}
	policy, err := s.repo.Active(ctx, stack, s.now())
	if err != nil {
		return ServiceState{}, err
	}
	state := ServiceState{StackPause: policy.StackPause, Source: policy.Source(service)}
	if pause, ok := policy.ServicePauses[service]; ok {
		state.ServicePause = &pause
	}
	state.Effective = state.Source != EffectiveSourceNone
	return state, nil
}

// SetStack creates or changes a direct stack pause.
func (s *Service) SetStack(ctx context.Context, stack string, expiresAt *time.Time) (bool, error) {
	if !s.stackConfigured(stack) {
		return false, ErrStackNotManaged
	}
	return s.set(ctx, Pause{Scope: ScopeStack, StackName: stack, ExpiresAt: normalizedExpiration(expiresAt)})
}

// SetService creates or changes a direct service pause.
func (s *Service) SetService(
	ctx context.Context, stack, service string, expiresAt *time.Time,
) (bool, error) {
	// An existing pause remains a valid GitOps-owned target after the service is
	// removed from Git, because that pause is what protects the live service
	// from pruning until an operator explicitly resumes it.
	if err := s.validateService(ctx, stack, service, true); err != nil {
		return false, err
	}
	return s.set(ctx, Pause{
		Scope: ScopeService, StackName: stack, ServiceName: service, ExpiresAt: normalizedExpiration(expiresAt),
	})
}

func (s *Service) set(ctx context.Context, pause Pause) (bool, error) {
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
			Scope: string(pause.Scope), StackName: pause.StackName,
			ServiceName: pause.ServiceName, ExpiresAt: pause.ExpiresAt,
		}})
	})
	return changed, err
}

// ResumeStack removes only the direct stack pause.
func (s *Service) ResumeStack(ctx context.Context, stack string) (bool, error) {
	return s.resume(ctx, ScopeStack, stack, "")
}

// ResumeService removes only the direct service pause.
func (s *Service) ResumeService(ctx context.Context, stack, service string) (bool, error) {
	return s.resume(ctx, ScopeService, stack, service)
}

func (s *Service) resume(ctx context.Context, scope Scope, stack, service string) (bool, error) {
	removed := false
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		removed, err = s.repo.Delete(ctx, scope, stack, service)
		if err != nil || !removed {
			return err
		}
		return s.events.Publish(ctx, &events.SyncResumed{SyncPauseEvent: events.SyncPauseEvent{
			Scope: string(scope), StackName: stack, ServiceName: service,
		}})
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
			removed, deleteErr := s.repo.Delete(ctx, pause.Scope, pause.StackName, pause.ServiceName)
			if deleteErr != nil {
				return deleteErr
			}
			if !removed {
				return fmt.Errorf("expired pause disappeared during transaction: %s/%s", pause.StackName, pause.ServiceName)
			}
			if err = s.events.Publish(ctx, &events.SyncPauseExpired{SyncPauseEvent: events.SyncPauseEvent{
				Scope: string(pause.Scope), StackName: pause.StackName,
				ServiceName: pause.ServiceName, ExpiresAt: pause.ExpiresAt,
			}}); err != nil {
				return err
			}
		}
		return nil
	})
	return expired, err
}

func (s *Service) validateService(ctx context.Context, stack, service string, allowExisting bool) error {
	stack, service = strings.TrimSpace(stack), strings.TrimSpace(service)
	if stack == "" || service == "" {
		return ErrServiceNotManaged
	}
	runtime, err := s.state.Read(ctx)
	if err != nil {
		return err
	}
	if stackState, ok := runtime.Stacks[stack]; ok {
		if _, ok = stackState.Services[service]; ok {
			return nil
		}
	}
	if allowExisting {
		_, found, directErr := s.repo.Direct(ctx, ScopeService, stack, service)
		if directErr != nil {
			return directErr
		}
		if found {
			return nil
		}
	}
	return ErrServiceNotManaged
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

// ManagedServices returns the current GitOps runtime service state for assistant target resolution.
func (s *Service) ManagedServices(ctx context.Context) (map[string][]string, error) {
	runtime, err := s.state.Read(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(runtime.Stacks))
	for stack, state := range runtime.Stacks {
		for service := range state.Services {
			result[stack] = append(result[stack], service)
		}
		sort.Strings(result[stack])
	}
	return result, nil
}

// PreserveNames returns the effective preserved names for desired and prior runtime services.
func PreserveNames(policy ActivePolicy, desired []string, prior gitopsmodel.Stack) []string {
	names := map[string]struct{}{}
	if policy.StackPause != nil {
		for _, name := range desired {
			names[name] = struct{}{}
		}
		for name := range prior.Services {
			names[name] = struct{}{}
		}
	} else {
		for name := range policy.ServicePauses {
			names[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
