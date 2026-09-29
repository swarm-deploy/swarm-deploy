package secrets

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestCollectorRefreshNormalizesAndPersistsSecretMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	manager.EXPECT().List(gomock.Any()).Return([]swarm.Secret{
		{
			ID: "secret-id", Name: "database-password", VersionID: 7, Driver: "vault",
			Labels: map[string]string{
				"external_path":                "kv/prod/database",
				"external_version_id":          "v7",
				"cloud-secrets.secret.managed": "true",
			},
		},
	}, nil)

	collector := NewCollector(manager, store)
	require.NoError(t, collector.refresh(ctx))

	secret, err := store.GetByName(ctx, "database-password")
	require.NoError(t, err)
	assert.Equal(t, "kv/prod/database", secret.ExternalPath)
	assert.Equal(t, "v7", secret.ExternalVersionID)
	assert.True(t, secret.Managed)
}

func TestCollectorRunDebouncesEvents(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	eventsCh := make(chan dockerevents.Message, 3)
	errorsCh := make(chan error)

	listCalls := make(chan struct{}, 2)
	manager.EXPECT().List(gomock.Any()).Times(2).DoAndReturn(func(context.Context) ([]swarm.Secret, error) {
		listCalls <- struct{}{}
		return []swarm.Secret{{ID: "secret-id", Name: "database-password"}}, nil
	})
	manager.EXPECT().Watch(gomock.Any()).Return(eventsCh, errorsCh, nil)

	collector := NewCollector(manager, store)
	collector.debounceDelay = 10 * time.Millisecond
	collector.reconcilePeriod = time.Hour
	collector.reconnectDelay = time.Hour
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()

	requireSignal(t, listCalls)
	eventsCh <- dockerevents.Message{}
	eventsCh <- dockerevents.Message{}
	eventsCh <- dockerevents.Message{}
	requireSignal(t, listCalls)
	assertNoSignal(t, listCalls)

	cancel()
	require.NoError(t, <-done)
}

func TestCollectorRunRefreshesAfterReconnect(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	firstEvents := make(chan dockerevents.Message)
	firstErrors := make(chan error, 1)
	secondEvents := make(chan dockerevents.Message)
	secondErrors := make(chan error)
	listCalls := make(chan struct{}, 2)

	manager.EXPECT().List(gomock.Any()).Times(2).DoAndReturn(func(context.Context) ([]swarm.Secret, error) {
		listCalls <- struct{}{}
		return []swarm.Secret{{ID: "secret-id", Name: "database-password"}}, nil
	})
	gomock.InOrder(
		manager.EXPECT().Watch(gomock.Any()).Return(firstEvents, firstErrors, nil),
		manager.EXPECT().Watch(gomock.Any()).Return(secondEvents, secondErrors, nil),
	)

	collector := NewCollector(manager, store)
	collector.reconnectDelay = time.Millisecond
	collector.reconcilePeriod = time.Hour
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()

	requireSignal(t, listCalls)
	firstErrors <- errors.New("connection lost")
	requireSignal(t, listCalls)

	cancel()
	require.NoError(t, <-done)
}

func TestCollectorWatchRefreshesOnPeriodicReconciliation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	eventsCh := make(chan dockerevents.Message)
	errorsCh := make(chan error)
	reconcile := make(chan time.Time, 1)
	refreshed := make(chan struct{}, 1)
	manager.EXPECT().Watch(gomock.Any()).Return(eventsCh, errorsCh, nil)
	manager.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Secret, error) {
		refreshed <- struct{}{}
		return []swarm.Secret{{ID: "secret-id", Name: "database-password"}}, nil
	})

	collector := NewCollector(manager, store)
	done := make(chan error, 1)
	go func() { done <- collector.watchOnce(ctx, reconcile, false) }()
	reconcile <- time.Now()
	requireSignal(t, refreshed)
	cancel()
	require.NoError(t, <-done)
}

func TestCollectorInitialRefreshFailureKeepsPersistedSnapshot(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := newTestStore(t)
	require.NoError(t, store.Replace(ctx, []model.Secret{{ID: "persisted-id", Name: "persisted-secret"}}))
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	eventsCh := make(chan dockerevents.Message)
	errorsCh := make(chan error)
	watchCalled := make(chan struct{})
	manager.EXPECT().List(gomock.Any()).Return(nil, errors.New("docker unavailable"))
	manager.EXPECT().Watch(gomock.Any()).DoAndReturn(
		func(context.Context) (<-chan dockerevents.Message, <-chan error, error) {
			close(watchCalled)
			return eventsCh, errorsCh, nil
		},
	)

	collector := NewCollector(manager, store)
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	select {
	case <-watchCalled:
	case <-time.After(time.Second):
		require.Fail(t, "collector did not start watching")
	}
	cancel()
	require.NoError(t, <-done)

	listed, err := store.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "persisted-secret", listed[0].Name)
}

func newTestStore(t *testing.T) *modelstore.FileStore {
	t.Helper()
	store, err := modelstore.NewFileStore(
		context.Background(),
		filepath.Join(t.TempDir(), "secrets.state.json"),
		fs.NewLocalFileSystem(),
	)
	require.NoError(t, err)
	return store
}

func requireSignal(t *testing.T, signals <-chan struct{}) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(time.Second):
		require.Fail(t, "timed out waiting for signal")
	}
}

func assertNoSignal(t *testing.T, signals <-chan struct{}) {
	t.Helper()
	select {
	case <-signals:
		assert.Fail(t, "unexpected signal")
	case <-time.After(50 * time.Millisecond):
	}
}
