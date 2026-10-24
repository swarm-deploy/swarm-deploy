package gitpull

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

const (
	repositoryA = "git.example.com/team/a"
	repositoryB = "git.example.com/team/b"
)

func TestTransition(t *testing.T) {
	failure := &Failure{Category: gitx.FailureAuthentication, Description: "Git repository authentication failed"}

	for _, tc := range []struct {
		name        string
		previous    *model.GitPullFailure
		observation Observation
		wantNext    *model.GitPullFailure
		wantEvents  []events.Event
	}{
		{
			name:        "success without previous failure is a no-op",
			observation: Observation{Repository: repositoryB, TriggerReason: "poll"},
		},
		{
			name:        "first failure is recorded",
			observation: Observation{Repository: repositoryB, TriggerReason: "poll", Failure: failure},
			wantNext:    &model.GitPullFailure{Repository: repositoryB},
			wantEvents: []events.Event{
				&events.GitPullFailed{
					Repository:    repositoryB,
					Category:      events.GitFailureCategory(gitx.FailureAuthentication),
					Description:   "Git repository authentication failed",
					TriggerReason: "poll",
				},
			},
		},
		{
			name:        "success after failure publishes recovery",
			previous:    &model.GitPullFailure{Repository: repositoryB},
			observation: Observation{Repository: repositoryB, TriggerReason: "webhook"},
			wantEvents: []events.Event{
				&events.GitPullSucceeded{Repository: repositoryB, TriggerReason: "webhook"},
			},
		},
		{
			name:        "repeated failure is published again",
			previous:    &model.GitPullFailure{Repository: repositoryB},
			observation: Observation{Repository: repositoryB, TriggerReason: "poll", Failure: failure},
			wantNext:    &model.GitPullFailure{Repository: repositoryB},
			wantEvents: []events.Event{
				&events.GitPullFailed{
					Repository:    repositoryB,
					Category:      events.GitFailureCategory(gitx.FailureAuthentication),
					Description:   "Git repository authentication failed",
					TriggerReason: "poll",
				},
			},
		},
		{
			name:        "success on changed repository clears failure without recovery",
			previous:    &model.GitPullFailure{Repository: repositoryA},
			observation: Observation{Repository: repositoryB, TriggerReason: "startup"},
			wantEvents: []events.Event{
				&events.ConfigurationChanged{PreviousRepository: repositoryA, Repository: repositoryB},
			},
		},
		{
			name:        "failure on changed repository replaces failure",
			previous:    &model.GitPullFailure{Repository: repositoryA},
			observation: Observation{Repository: repositoryB, TriggerReason: "startup", Failure: failure},
			wantNext:    &model.GitPullFailure{Repository: repositoryB},
			wantEvents: []events.Event{
				&events.ConfigurationChanged{PreviousRepository: repositoryA, Repository: repositoryB},
				&events.GitPullFailed{
					Repository:    repositoryB,
					Category:      events.GitFailureCategory(gitx.FailureAuthentication),
					Description:   "Git repository authentication failed",
					TriggerReason: "startup",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, pending := transition(tc.previous, tc.observation)

			assert.Equal(t, tc.wantNext, next, "next failure state")
			assert.Equal(t, tc.wantEvents, pending, "events to publish")
		})
	}
}

func TestMonitorObserve(t *testing.T) {
	errOutbox := errors.New("outbox unavailable")
	failure := &Failure{Category: gitx.FailureAuthentication, Description: "Git repository authentication failed"}

	for _, tc := range []struct {
		name        string
		previous    *model.GitPullFailure
		observation Observation
		// publishErrs are returned by consecutive Publish calls; nil entries succeed.
		publishErrs []error
		wantErr     error
		wantState   *model.GitPullFailure
	}{
		{
			name:        "success without previous failure does not publish",
			observation: Observation{Repository: repositoryB, TriggerReason: "poll"},
		},
		{
			name:        "failure is persisted with its event",
			observation: Observation{Repository: repositoryB, TriggerReason: "manual", Failure: failure},
			publishErrs: []error{nil},
			wantState:   &model.GitPullFailure{Repository: repositoryB},
		},
		{
			name:        "failure state rolls back when its event cannot be published",
			observation: Observation{Repository: repositoryB, TriggerReason: "manual", Failure: failure},
			publishErrs: []error{errOutbox},
			wantErr:     errOutbox,
		},
		{
			name:        "recovery clears failure",
			previous:    &model.GitPullFailure{Repository: repositoryB},
			observation: Observation{Repository: repositoryB, TriggerReason: "startup"},
			publishErrs: []error{nil},
		},
		{
			name:        "recovery state rolls back when its event cannot be published",
			previous:    &model.GitPullFailure{Repository: repositoryB},
			observation: Observation{Repository: repositoryB, TriggerReason: "webhook"},
			publishErrs: []error{errOutbox},
			wantErr:     errOutbox,
			wantState:   &model.GitPullFailure{Repository: repositoryB},
		},
		{
			name:        "repository change rolls back when its event cannot be published",
			previous:    &model.GitPullFailure{Repository: repositoryA},
			observation: Observation{Repository: repositoryB, TriggerReason: "startup"},
			publishErrs: []error{errOutbox},
			wantErr:     errOutbox,
			wantState:   &model.GitPullFailure{Repository: repositoryA},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, store := newSQLState(t)
			require.NoError(t, store.Update(t.Context(), func(state *model.Runtime) {
				state.GitPullFailure = tc.previous
			}), "seed previous failure")

			publisher := dispatcher.NewMockDispatcher(ctrl)
			calls := make([]any, 0, len(tc.publishErrs))
			for _, publishErr := range tc.publishErrs {
				calls = append(calls, publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(publishErr))
			}
			gomock.InOrder(calls...)

			err := NewMonitor(db, store, publisher).Observe(t.Context(), tc.observation)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr, "observe error")
			} else {
				require.NoError(t, err, "observe")
			}
			assert.Equal(t, tc.wantState, readState(t, store).GitPullFailure, "persisted failure")
		})
	}
}

func TestMonitorRecoveryStateSurvivesRestart(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	gomock.InOrder(
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).Return(nil),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullSucceeded{})).Return(nil),
	)

	require.NoError(t, NewMonitor(db, store, publisher).Observe(t.Context(), Observation{
		Repository:    repositoryA,
		TriggerReason: "poll",
		Failure:       &Failure{Category: gitx.FailureAuthentication, Description: "Git repository authentication failed"},
	}), "observe failure")
	require.NotNil(t, readState(t, store).GitPullFailure, "failure must be persisted")

	restarted := NewMonitor(db, modelstore.NewSQLStore(db), publisher)
	require.NoError(t, restarted.Observe(t.Context(), Observation{
		Repository:    repositoryA,
		TriggerReason: "startup",
	}), "observe recovery after restart")
	assert.Nil(t, readState(t, store).GitPullFailure, "recovery must clear persisted failure")
}

func newSQLState(t *testing.T) (*storage.Database, *modelstore.SQLStore) {
	t.Helper()
	db := testutil.OpenSQLite(t)
	return db, modelstore.NewSQLStore(db)
}

func readState(t *testing.T, store *modelstore.SQLStore) model.Runtime {
	t.Helper()
	state, err := store.Read(t.Context())
	require.NoError(t, err, "read state")
	return state
}
