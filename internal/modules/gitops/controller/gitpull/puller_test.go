package gitpull

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"go.uber.org/mock/gomock"
)

func TestPullerPullOnChangedRepository(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pullErr    error
		wantFailed bool
	}{
		{name: "success removes previous failure without recovery"},
		{name: "failure replaces previous failure", pullErr: errors.New("unavailable"), wantFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, store := newSQLState(t)
			require.NoError(t, store.Update(t.Context(), func(state *model.Runtime) {
				state.GitPullFailure = &model.GitPullFailure{Repository: "git.example.com/team/old"}
			}), "seed previous failure")

			repository := gitx.NewMockRepository(ctrl)
			repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{NewRevision: "new"}, tc.pullErr)

			publisher := dispatcher.NewMockDispatcher(ctrl)
			calls := []any{
				publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ConfigurationChanged{})).DoAndReturn(
					func(_ context.Context, event events.Event) error {
						changed := event.(*events.ConfigurationChanged)
						assert.Equal(t, "git.example.com/team/old", changed.PreviousRepository)
						assert.Equal(t, "git.example.com/team/new", changed.Repository)
						return nil
					},
				),
			}
			if tc.wantFailed {
				calls = append(calls,
					publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).DoAndReturn(
						func(_ context.Context, event events.Event) error {
							assert.Equal(t, "git.example.com/team/new", event.(*events.GitPullFailed).Repository)
							return nil
						},
					),
				)
			}
			gomock.InOrder(calls...)

			puller := NewPuller(repository, NewMonitor(db, store, publisher),
				metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_git_changed_repository_" + fmt.Sprint(tc.wantFailed)}).Git,
				config.GitSpec{Repository: "https://git.example.com/team/new.git"},
			)

			_, err := puller.Pull(t.Context(), "startup")

			failure := readState(t, store).GitPullFailure
			if tc.wantFailed {
				require.Error(t, err, "pull")
				require.NotNil(t, failure, "persisted failure")
				assert.Equal(t, "git.example.com/team/new", failure.Repository)
			} else {
				require.NoError(t, err, "pull")
				assert.Nil(t, failure, "persisted failure")
			}
		})
	}
}

func TestPullerRecoveryPublicationFailureDoesNotChangeSuccessfulPull(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	require.NoError(t, store.Update(t.Context(), func(state *model.Runtime) {
		state.GitPullFailure = &model.GitPullFailure{Repository: "git.example.com/team/app"}
	}), "seed previous failure")

	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{OldRevision: "one", NewRevision: "two", Updated: true}, nil)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullSucceeded{})).
		Return(errors.New("outbox unavailable"))

	puller := NewPuller(repository, NewMonitor(db, store, publisher),
		metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_git_pull_recovery_publication"}).Git,
		config.GitSpec{Repository: "https://git.example.com/team/app.git"},
	)

	result, err := puller.Pull(t.Context(), "poll")

	require.NoError(t, err, "pull")
	assert.True(t, result.Updated)
	assert.Equal(t, "two", result.NewRevision)
	assert.NotNil(t, readState(t, store).GitPullFailure,
		"failed recovery publication must remain retryable after a successful pull")
}

func TestPullerFailureMonitoringCoversEveryPullTrigger(t *testing.T) {
	for _, reason := range []string{"startup", "poll", "manual", "webhook"} {
		t.Run(reason, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, store := newSQLState(t)
			repository := gitx.NewMockRepository(ctrl)
			repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{},
				errors.New("https://robot:private-token@git.example.com failed"))
			publisher := dispatcher.NewMockDispatcher(ctrl)
			publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					failed := event.(*events.GitPullFailed)
					assert.Equal(t, reason, failed.TriggerReason)
					assert.Equal(t, "git.example.com/team/app", failed.Repository)
					assert.NotContains(t, failed.Description, "private-token")
					return nil
				},
			)

			puller := NewPuller(repository, NewMonitor(db, store, publisher),
				metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_git_pull_trigger_" + reason}).Git,
				config.GitSpec{Repository: "https://robot:private-token@git.example.com/team/app.git"},
			)

			_, err := puller.Pull(t.Context(), reason)

			require.Error(t, err, "pull")
			assert.NotContains(t, err.Error(), "private-token")
		})
	}
}

func TestPullerFailureWithMalformedCredentialURLPublishesSafeIdentifier(t *testing.T) {
	const secret = "private-token"
	for index, remote := range []string{
		"https://robot:" + secret + "%zz@git.example.com/team/app.git",
		"https:robot:" + secret + "@git.example.com/team/app.git",
		"https:robot:" + secret + "%zz@git.example.com/team/app.git",
		"https:/",
	} {
		t.Run(remote, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, store := newSQLState(t)
			repository := gitx.NewMockRepository(ctrl)
			repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{}, errors.New("remote unavailable"))
			publisher := dispatcher.NewMockDispatcher(ctrl)
			publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					failed := event.(*events.GitPullFailed)
					assert.Regexp(t, `^repository-[0-9a-f]{16}$`, failed.Repository)
					assert.NotContains(t, failed.Repository, secret)
					return nil
				},
			)

			puller := NewPuller(repository, NewMonitor(db, store, publisher),
				metrics.NewGroup(metrics.CreateGroupParams{
					Namespace: fmt.Sprintf("test_git_pull_malformed_remote_%d", index),
				}).Git,
				config.GitSpec{Repository: remote},
			)

			_, err := puller.Pull(t.Context(), "startup")

			require.Error(t, err, "pull")
			assert.NotContains(t, err.Error(), secret)
			failure := readState(t, store).GitPullFailure
			require.NotNil(t, failure, "persisted failure")
			assert.Regexp(t, `^repository-[0-9a-f]{16}$`, failure.Repository)
			assert.NotContains(t, failure.Repository, secret)
		})
	}
}
