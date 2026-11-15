package syncpause

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

func TestServiceCalculatesIndependentEffectivePausePrecedence(t *testing.T) {
	service, publisher, now := newPauseService(t)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.SyncPaused{})).Return(nil).Times(3)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.SyncResumed{})).Return(nil).Times(3)

	changed, err := service.SetStack(t.Context(), "app", nil)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = service.SetStack(t.Context(), "app", nil)
	require.NoError(t, err)
	assert.False(t, changed, "idempotent writes must not publish")
	expires := now.Add(time.Hour)
	changed, err = service.SetService(t.Context(), "app", "api", &expires)
	require.NoError(t, err)
	assert.True(t, changed)

	stack, err := service.GetStack(t.Context(), "app")
	require.NoError(t, err)
	require.NotNil(t, stack.StackPause)
	assert.Len(t, stack.ServicePauses, 1)
	assert.ElementsMatch(t, []string{"api", "worker"}, stack.EffectivelyPausedServices)
	api, err := service.GetService(t.Context(), "app", "api")
	require.NoError(t, err)
	assert.True(t, api.Effective)
	assert.Equal(t, EffectiveSourceBoth, api.Source)

	removed, err := service.ResumeStack(t.Context(), "app")
	require.NoError(t, err)
	assert.True(t, removed)
	api, err = service.GetService(t.Context(), "app", "api")
	require.NoError(t, err)
	assert.Equal(t, EffectiveSourceService, api.Source, "stack resume must preserve service pauses")

	_, err = service.SetStack(t.Context(), "app", nil)
	require.NoError(t, err)
	removed, err = service.ResumeService(t.Context(), "app", "api")
	require.NoError(t, err)
	assert.True(t, removed)
	api, err = service.GetService(t.Context(), "app", "api")
	require.NoError(t, err)
	assert.True(t, api.Effective)
	assert.Equal(t, EffectiveSourceStack, api.Source, "removing service pause under stack pause stays effective")
	_, err = service.ResumeStack(t.Context(), "app")
	require.NoError(t, err)
}

func TestServiceExpiresOnceDuringExplicitProcessing(t *testing.T) {
	service, publisher, now := newPauseService(t)
	expires := now.Add(time.Hour)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.SyncPaused{})).Return(nil)
	require.NoError(t, func() error {
		_, err := service.SetService(t.Context(), "app", "api", &expires)
		return err
	}())

	expiredEvents := 0
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.SyncPauseExpired{})).
		DoAndReturn(func(_ context.Context, event events.Event) error {
			expiredEvents++
			assert.Equal(t, "api", event.(*events.SyncPauseExpired).ServiceName)
			return nil
		})
	service.now = func() time.Time { return expires.Add(time.Nanosecond) }
	expired, err := service.Expire(t.Context())
	require.NoError(t, err)
	assert.Len(t, expired, 1)
	expired, err = service.Expire(t.Context())
	require.NoError(t, err)
	assert.Empty(t, expired)
	assert.Equal(t, 1, expiredEvents)
}

func TestExpirationRollsBackWhenEventCannotBePersisted(t *testing.T) {
	service, publisher, now := newPauseService(t)
	expires := now.Add(time.Hour)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.SyncPaused{})).Return(nil)
	_, err := service.SetService(t.Context(), "app", "api", &expires)
	require.NoError(t, err)

	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.SyncPauseExpired{})).
		Return(errors.New("outbox unavailable"))
	service.now = func() time.Time { return expires.Add(time.Second) }
	_, err = service.Expire(t.Context())
	require.Error(t, err)
	_, found, err := service.repo.Direct(t.Context(), ScopeService, "app", "api")
	require.NoError(t, err)
	assert.True(t, found, "policy transition must roll back with the event")
}

func TestPausePolicyDoesNotChangeSyncStatus(t *testing.T) {
	service, publisher, _ := newPauseService(t)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.SyncPaused{})).Return(nil)
	_, err := service.SetService(t.Context(), "app", "api", nil)
	require.NoError(t, err)

	runtime, err := service.state.Read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, gitopsmodel.SyncStatus(gitopsmodel.SyncStatusOutOfSync),
		runtime.Stacks["app"].Services["api"].SyncStatus)
}

func newPauseService(t *testing.T) (*Service, *dispatcher.MockDispatcher, time.Time) {
	t.Helper()
	db := testutil.OpenSQLite(t)
	state := modelstore.NewMemoryStore()
	require.NoError(t, state.Update(t.Context(), func(runtime *gitopsmodel.Runtime) {
		runtime.Stacks["app"] = gitopsmodel.Stack{Services: map[string]gitopsmodel.Service{
			"api": {SyncStatus: gitopsmodel.SyncStatusOutOfSync}, "worker": {},
		}}
	}))
	provider := NewMockStackProvider(gomock.NewController(t))
	provider.EXPECT().Stacks().Return([]config.StackSpec{{Name: "app"}}).AnyTimes()
	publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	service := NewService(db, state, provider, publisher)
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	return service, publisher, now
}
