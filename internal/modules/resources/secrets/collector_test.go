package secrets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

func TestCollectorRefreshNormalizesAndPersistsSecretMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	manager.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{}).Return([]swarm.Secret{
		{
			ID: "secret-id", Name: "database-password", VersionID: 7, Driver: "vault",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			Labels: map[string]string{
				"description":                  "Primary database credentials",
				"external_path":                "kv/prod/database",
				"external_version_id":          "v7",
				"cloud-secrets.secret.managed": "true",
			},
		},
	}, nil)

	collector := NewCollector(manager, store, newTestSubscription())
	require.NoError(t, collector.refresh(ctx))

	secret, err := store.GetByName(ctx, "database-password")
	require.NoError(t, err)
	assert.Equal(t, "Primary database credentials", secret.Description)
	assert.Equal(t, "kv/prod/database", secret.ExternalPath)
	assert.Equal(t, "v7", secret.ExternalVersionID)
	assert.True(t, secret.Managed)
}

func TestCollectorRunDebouncesEvents(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	eventsCh := make(chan swarm.SecretEvent, 3)
	resyncCh := make(chan swarm.ResyncEvent)

	listCalls := make(chan struct{}, 2)
	manager.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{}).Times(2).DoAndReturn(func(
		context.Context,
		swarm.ListSecretsFilter,
	) ([]swarm.Secret, error) {
		listCalls <- struct{}{}
		now := time.Now().UTC()
		return []swarm.Secret{{ID: "secret-id", Name: "database-password", CreatedAt: now, UpdatedAt: now}}, nil
	})

	collector := NewCollector(manager, store, swarm.EventSubscription[swarm.SecretEvent]{
		Events: eventsCh,
		Resync: resyncCh,
	})
	collector.debounceDelay = 10 * time.Millisecond
	collector.reconcilePeriod = time.Hour
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()

	requireSignal(t, listCalls)
	eventsCh <- swarm.SecretEvent{}
	eventsCh <- swarm.SecretEvent{}
	eventsCh <- swarm.SecretEvent{}
	requireSignal(t, listCalls)
	assertNoSignal(t, listCalls)

	cancel()
	require.NoError(t, <-done)
}

func TestCollectorRunRefreshesOnResync(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	eventsCh := make(chan swarm.SecretEvent)
	resyncCh := make(chan swarm.ResyncEvent, 1)
	listCalls := make(chan struct{}, 2)

	manager.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{}).Times(2).DoAndReturn(func(
		context.Context,
		swarm.ListSecretsFilter,
	) ([]swarm.Secret, error) {
		listCalls <- struct{}{}
		now := time.Now().UTC()
		return []swarm.Secret{{ID: "secret-id", Name: "database-password", CreatedAt: now, UpdatedAt: now}}, nil
	})

	collector := NewCollector(manager, store, swarm.EventSubscription[swarm.SecretEvent]{
		Events: eventsCh,
		Resync: resyncCh,
	})
	collector.reconcilePeriod = time.Hour
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()

	requireSignal(t, listCalls)
	resyncCh <- swarm.ResyncEvent{}
	requireSignal(t, listCalls)

	cancel()
	require.NoError(t, <-done)
}

func TestCollectorWatchRefreshesOnPeriodicReconciliation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := newTestStore(t)
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	reconcile := make(chan time.Time, 1)
	refreshed := make(chan struct{}, 1)
	manager.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{}).DoAndReturn(func(
		context.Context,
		swarm.ListSecretsFilter,
	) ([]swarm.Secret, error) {
		refreshed <- struct{}{}
		now := time.Now().UTC()
		return []swarm.Secret{{ID: "secret-id", Name: "database-password", CreatedAt: now, UpdatedAt: now}}, nil
	})

	collector := NewCollector(manager, store, newTestSubscription())
	done := make(chan error, 1)
	go func() { done <- collector.watch(ctx, reconcile) }()
	reconcile <- time.Now()
	requireSignal(t, refreshed)
	cancel()
	require.NoError(t, <-done)
}

func TestCollectorInitialRefreshFailureKeepsPersistedSnapshot(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, store.Replace(ctx, []model.Secret{{
		ID: "persisted-id", Name: "persisted-secret", CreatedAt: now, UpdatedAt: now,
	}}))
	manager := swarm.NewMockSecretManager(gomock.NewController(t))
	listCalled := make(chan struct{})
	manager.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{}).DoAndReturn(func(
		context.Context,
		swarm.ListSecretsFilter,
	) ([]swarm.Secret, error) {
		close(listCalled)
		return nil, errors.New("docker unavailable")
	})

	collector := NewCollector(manager, store, newTestSubscription())
	collector.reconcilePeriod = time.Hour
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()

	requireSignal(t, listCalled)
	cancel()
	require.NoError(t, <-done)

	listed, err := store.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "persisted-secret", listed[0].Name)
}

func newTestSubscription() swarm.EventSubscription[swarm.SecretEvent] {
	return swarm.EventSubscription[swarm.SecretEvent]{
		Events: make(chan swarm.SecretEvent),
		Resync: make(chan swarm.ResyncEvent),
	}
}

func newTestStore(t *testing.T) *modelstore.SQLStore {
	t.Helper()
	return modelstore.NewSQLStore(testutil.OpenSQLite(t))
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
