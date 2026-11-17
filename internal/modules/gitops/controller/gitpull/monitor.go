// Package gitpull pulls the GitOps repository and tracks pull failures and recoveries.
package gitpull

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// Observation is the outcome of one Git pull attempt.
type Observation struct {
	// Availability is a confirmed remote observation; nil leaves remote access unknown or unchanged.
	Availability *bool
	// Repository is the credential-free repository identifier.
	Repository string
	// TriggerReason is the sync trigger that caused the pull.
	TriggerReason string
	// Failure describes a failed pull. Nil means the pull succeeded.
	Failure *Failure
}

// Failure describes why a Git pull failed.
type Failure struct {
	// Category is the classified failure category.
	Category gitx.FailureCategory
	// Description is a credential-free diagnostic message.
	Description string
}

// Monitor persists confirmed remote access and the latest Git pull failure, and publishes failure, recovery
// and repository change events. The persisted failure lets recovery be detected after restart.
type Monitor struct {
	db    storage.Database
	store modelstore.Store
	event dispatcher.Dispatcher
}

// NewMonitor creates a Monitor.
func NewMonitor(db storage.Database, store modelstore.Store, event dispatcher.Dispatcher) *Monitor {
	return &Monitor{db: db, store: store, event: event}
}

// Observe records the pull outcome. State and events are written in one transaction,
// so a failed event publication rolls the state back and the transition is retried on the next pull.
func (m *Monitor) Observe(ctx context.Context, observation Observation) error {
	return m.db.WithinTransaction(ctx, func(ctx context.Context) error {
		state, err := m.store.Read(ctx)
		if err != nil {
			return fmt.Errorf("read Git pull state: %w", err)
		}

		previous := state.GitPullFailure
		next, pending := transition(previous, observation)
		access := observedAccess(state.GitAccess, observation)
		if previous == nil && next == nil && access == state.GitAccess {
			return nil
		}

		if err = m.store.Update(ctx, func(runtime *model.Runtime) {
			runtime.GitPullFailure = next
			runtime.GitAccess = access
		}); err != nil {
			return fmt.Errorf("persist Git pull state: %w", err)
		}

		for _, event := range pending {
			if err = m.event.Publish(ctx, event); err != nil {
				return err
			}
		}

		return nil
	})
}

func observedAccess(previous *model.GitAccess, observation Observation) *model.GitAccess {
	if observation.Availability == nil {
		return previous
	}
	if previous != nil && previous.Repository == observation.Repository &&
		previous.Available == *observation.Availability {
		return previous
	}
	return &model.GitAccess{Repository: observation.Repository, Available: *observation.Availability}
}

// transition returns the next persisted failure and the events to publish for an observation.
//
//	previous | outcome     | next | events
//	---------+-------------+------+------------------------------------------
//	-        | success     | -    | -
//	-        | failure B   | B    | GitPullFailed(B)
//	B        | success B   | -    | GitPullSucceeded(B)
//	B        | failure B   | B    | GitPullFailed(B)
//	A        | success B   | -    | ConfigurationChanged(A->B)
//	A        | failure B   | B    | ConfigurationChanged(A->B), GitPullFailed(B)
//
// Events are returned in publication order.
func transition(previous *model.GitPullFailure, observation Observation) (*model.GitPullFailure, []events.Event) {
	var pending []events.Event
	repositoryChanged := previous != nil && previous.Repository != observation.Repository
	if repositoryChanged {
		pending = append(pending, &events.ConfigurationChanged{
			PreviousRepository: previous.Repository,
			Repository:         observation.Repository,
		})
	}

	if observation.Failure != nil {
		pending = append(pending, &events.GitPullFailed{
			Repository:    observation.Repository,
			Category:      events.GitFailureCategory(observation.Failure.Category),
			Description:   observation.Failure.Description,
			TriggerReason: observation.TriggerReason,
		})
		return &model.GitPullFailure{Repository: observation.Repository}, pending
	}

	if previous != nil && !repositoryChanged {
		pending = append(pending, &events.GitPullSucceeded{
			Repository:    observation.Repository,
			TriggerReason: observation.TriggerReason,
		})
	}

	return nil, pending
}
