package networkloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestCoordinatorStoresState(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockNetworkManager(ctrl)

	manager.EXPECT().
		Get(gomock.Any(), "app_backend").
		Return(swarm.Network{}, swarm.ErrNetworkNotFound)
	manager.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		Return("created-id", nil)

	db, store := newNetworkSQLState(t)
	configuration := newNetworkTestConfigStore(t, "app_backend")
	c := NewCoordinator(db, configuration, store, New(manager), &dispatcher.NopDispatcher{})

	err := c.Reconcile(context.Background(), "commit-1")
	require.NoError(t, err, "sync networks")

	state := readNetworkSQLState(t, store)
	require.Len(t, state.Networks, 1, "expected one stored network")

	networkState := state.Networks["app_backend"]
	assert.Equal(t, "overlay", networkState.Driver, "unexpected network driver")
	assert.Equal(t, "commit-1", networkState.LastCommit, "unexpected network commit")
	assert.Equal(t, "success", networkState.LastStatus, "unexpected network status")
	assert.Empty(t, networkState.LastError, "expected empty network error")
	assert.False(t, networkState.LastSyncAt.IsZero(), "expected sync timestamp")
}

func TestCoordinatorStoresFailedState(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockNetworkManager(ctrl)

	manager.EXPECT().
		Get(gomock.Any(), "app_backend").
		Return(swarm.Network{
			Name:   "app_backend",
			Driver: "overlay",
		}, nil)

	db, store := newNetworkSQLState(t)
	configuration := newNetworkTestConfigStore(t, "app_backend")
	c := NewCoordinator(db, configuration, store, New(manager), &dispatcher.NopDispatcher{})

	err := c.Reconcile(context.Background(), "commit-2")
	require.Error(t, err, "expected sync error")

	state := readNetworkSQLState(t, store)
	require.Len(t, state.Networks, 1, "expected one stored network")

	networkState := state.Networks["app_backend"]
	assert.Equal(t, "failed", networkState.LastStatus, "unexpected network status")
	assert.Contains(t, networkState.LastError, "not managed by swarm-deploy", "unexpected network error")
	assert.Equal(t, "commit-2", networkState.LastCommit, "unexpected network commit")
}

func TestCoordinatorClearsStateWhenNetworksListIsEmpty(t *testing.T) {
	db, store := newNetworkSQLState(t)
	store.Update(context.Background(), func(s *model.Runtime) {
		s.Networks["legacy"] = model.Network{
			Driver:     "overlay",
			LastStatus: "success",
		}
	})

	c := NewCoordinator(db, newNetworkTestConfigStore(t, ""), store,
		New(swarm.NewMockNetworkManager(gomock.NewController(t))), &dispatcher.NopDispatcher{})

	err := c.Reconcile(context.Background(), "commit-3")
	require.NoError(t, err, "sync networks")

	state := readNetworkSQLState(t, store)
	assert.Empty(t, state.Networks, "expected cleared network state")
}

func TestCoordinatorSkipsReconcileWhenStateAlreadySyncedForCommit(t *testing.T) {
	db, store := newNetworkSQLState(t)
	store.Update(context.Background(), func(s *model.Runtime) {
		s.Networks["app_backend"] = model.Network{
			Driver:     "overlay",
			LastCommit: "commit-4",
			LastStatus: "success",
			LastError:  "",
		}
	})

	ctrl := gomock.NewController(t)
	manager := swarm.NewMockNetworkManager(ctrl)

	c := NewCoordinator(db, newNetworkTestConfigStore(t, "app_backend"), store,
		New(manager), &dispatcher.NopDispatcher{})

	err := c.Reconcile(context.Background(), "commit-4")
	require.NoError(t, err, "sync networks")

	state := readNetworkSQLState(t, store)
	networkState := state.Networks["app_backend"]
	assert.Equal(t, "success", networkState.LastStatus, "expected previous successful status preserved")
	assert.Equal(t, "commit-4", networkState.LastCommit, "expected previous commit preserved")
}

func newNetworkTestConfigStore(t *testing.T, network string) *configstore.ConfigStore {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"),
		[]byte("stacks:\n  - name: app\n    composeFile: app.yaml\n"), 0o600))
	cfg := &config.Config{Spec: config.Spec{StacksSource: config.StacksSourceSpec{File: "stacks.yaml"}}}
	if network != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "networks.yaml"),
			[]byte("networks:\n  - name: "+network+"\n"), 0o600))
		cfg.Spec.NetworksSource.File = "networks.yaml"
	}
	_, err := cfg.Reload(dir)
	require.NoError(t, err)
	return configstore.New(cfg, &dispatcher.NopDispatcher{})
}

func newNetworkSQLState(t *testing.T) (storage.Database, *modelstore.SQLStore) {
	t.Helper()
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, modelstore.NewSQLStore(db)
}

func readNetworkSQLState(t *testing.T, store *modelstore.SQLStore) model.Runtime {
	t.Helper()
	state, err := store.Read(t.Context())
	require.NoError(t, err)
	return state
}
