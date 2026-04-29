package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/controller/statem"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type fakeNetworkManager struct {
	getNetwork swarm.Network
	getErr     error
	getCalls   int

	createReq   *swarm.CreateNetworkRequest
	createErr   error
	createCalls int
}

func (m *fakeNetworkManager) Get(context.Context, string) (swarm.Network, error) {
	m.getCalls++
	return m.getNetwork, m.getErr
}

func (m *fakeNetworkManager) Create(_ context.Context, req swarm.CreateNetworkRequest) (string, error) {
	m.createCalls++
	m.createReq = &req
	if m.createErr != nil {
		return "", m.createErr
	}
	return "created-id", nil
}

func TestNetworkReconcilerReconcileCreatesManagedNetwork(t *testing.T) {
	manager := &fakeNetworkManager{
		getErr: swarm.ErrNetworkNotFound,
	}

	reconciler := newNetworkReconciler(manager)
	skipped, err := reconciler.Reconcile(context.Background(), config.NetworkSpec{
		Name:       "app_backend",
		Driver:     "overlay",
		Attachable: true,
		Labels: map[string]string{
			"team": "platform",
		},
		Options: map[string]string{
			"encrypted": "true",
		},
	})

	require.NoError(t, err, "reconcile network")
	assert.False(t, skipped, "expected created network")
	require.NotNil(t, manager.createReq, "expected create request")
	assert.Equal(t, "app_backend", manager.createReq.Name, "unexpected network name")
	assert.Equal(t, "overlay", manager.createReq.Driver, "unexpected driver")
	assert.True(t, manager.createReq.Attachable, "unexpected attachable flag")
	assert.Equal(
		t,
		managedNetworkLabelValue,
		manager.createReq.Labels[managedNetworkLabelKey],
		"expected managed label",
	)
}

func TestNetworkReconcilerReconcileFailsWhenExistingNetworkIsNotManaged(t *testing.T) {
	manager := &fakeNetworkManager{
		getNetwork: swarm.Network{
			Name:   "app_backend",
			Driver: "overlay",
		},
	}

	reconciler := newNetworkReconciler(manager)
	_, err := reconciler.Reconcile(context.Background(), config.NetworkSpec{
		Name:   "app_backend",
		Driver: "overlay",
	})

	require.Error(t, err, "expected ownership error")
	assert.Contains(t, err.Error(), "not managed by swarm-deploy", "unexpected error")
}

func TestNetworkReconcilerReconcileFailsOnManagedLabelOverride(t *testing.T) {
	manager := &fakeNetworkManager{
		getErr: swarm.ErrNetworkNotFound,
	}

	reconciler := newNetworkReconciler(manager)
	_, err := reconciler.Reconcile(context.Background(), config.NetworkSpec{
		Name:   "app_backend",
		Driver: "overlay",
		Labels: map[string]string{
			managedNetworkLabelKey: "false",
		},
	})

	require.Error(t, err, "expected validation error")
	assert.Contains(t, err.Error(), `label "org.swarm-deploy.network.managed" must be "true"`, "unexpected error")
}

func TestNetworkReconcilerReconcileSkipsMatchingManagedNetwork(t *testing.T) {
	manager := &fakeNetworkManager{
		getNetwork: swarm.Network{
			Name:       "app_backend",
			Driver:     "overlay",
			Attachable: true,
			Internal:   true,
			Labels: map[string]string{
				managedNetworkLabelKey: managedNetworkLabelValue,
				"team":                 "platform",
			},
			Options: map[string]string{
				"encrypted": "true",
				"mtu":       "1450",
			},
		},
	}

	reconciler := newNetworkReconciler(manager)
	skipped, err := reconciler.Reconcile(context.Background(), config.NetworkSpec{
		Name:       "app_backend",
		Driver:     "overlay",
		Attachable: true,
		Internal:   true,
		Labels: map[string]string{
			"team": "platform",
		},
		Options: map[string]string{
			"encrypted": "true",
		},
	})

	require.NoError(t, err, "reconcile network")
	assert.True(t, skipped, "expected skip")
	assert.Nil(t, manager.createReq, "create should not be called")
}

func TestControllerSyncNetworksStoresState(t *testing.T) {
	manager := &fakeNetworkManager{
		getErr: swarm.ErrNetworkNotFound,
	}

	store := statem.NewMemoryStore()
	c := &Controller{
		cfg: &config.Config{
			Spec: config.Spec{
				Networks: []config.NetworkSpec{
					{
						Name:   "app_backend",
						Driver: "overlay",
					},
				},
			},
		},
		networkReconciler: newNetworkReconciler(manager),
		stateStore:        store,
	}

	err := c.syncNetworks(context.Background(), "commit-1")
	require.NoError(t, err, "sync networks")

	state := store.Get()
	require.Len(t, state.Networks, 1, "expected one stored network")

	networkState := state.Networks["app_backend"]
	assert.Equal(t, "overlay", networkState.Driver, "unexpected network driver")
	assert.Equal(t, "commit-1", networkState.LastCommit, "unexpected network commit")
	assert.Equal(t, "success", networkState.LastStatus, "unexpected network status")
	assert.Empty(t, networkState.LastError, "expected empty network error")
	assert.False(t, networkState.LastSyncAt.IsZero(), "expected sync timestamp")
}

func TestControllerSyncNetworksStoresFailedState(t *testing.T) {
	manager := &fakeNetworkManager{
		getNetwork: swarm.Network{
			Name:   "app_backend",
			Driver: "overlay",
		},
	}

	store := statem.NewMemoryStore()
	c := &Controller{
		cfg: &config.Config{
			Spec: config.Spec{
				Networks: []config.NetworkSpec{
					{
						Name:   "app_backend",
						Driver: "overlay",
					},
				},
			},
		},
		networkReconciler: newNetworkReconciler(manager),
		stateStore:        store,
	}

	err := c.syncNetworks(context.Background(), "commit-2")
	require.Error(t, err, "expected sync error")

	state := store.Get()
	require.Len(t, state.Networks, 1, "expected one stored network")

	networkState := state.Networks["app_backend"]
	assert.Equal(t, "failed", networkState.LastStatus, "unexpected network status")
	assert.Contains(t, networkState.LastError, "not managed by swarm-deploy", "unexpected network error")
	assert.Equal(t, "commit-2", networkState.LastCommit, "unexpected network commit")
}

func TestControllerSyncNetworksClearsStateWhenNetworksListIsEmpty(t *testing.T) {
	store := statem.NewMemoryStore()
	store.Update(func(s *statem.Runtime) {
		s.Networks["legacy"] = statem.Network{
			Driver:     "overlay",
			LastStatus: "success",
		}
	})

	c := &Controller{
		cfg: &config.Config{
			Spec: config.Spec{
				Networks: nil,
			},
		},
		networkReconciler: newNetworkReconciler(&fakeNetworkManager{}),
		stateStore:        store,
	}

	err := c.syncNetworks(context.Background(), "commit-3")
	require.NoError(t, err, "sync networks")

	state := store.Get()
	assert.Empty(t, state.Networks, "expected cleared network state")
}

func TestControllerSyncNetworksSkipsReconcileWhenStateAlreadySyncedForCommit(t *testing.T) {
	store := statem.NewMemoryStore()
	store.Update(func(s *statem.Runtime) {
		s.Networks["app_backend"] = statem.Network{
			Driver:     "overlay",
			LastCommit: "commit-4",
			LastStatus: "success",
			LastError:  "",
		}
	})

	manager := &fakeNetworkManager{
		getErr: swarm.ErrNetworkNotFound,
	}

	c := &Controller{
		cfg: &config.Config{
			Spec: config.Spec{
				Networks: []config.NetworkSpec{
					{
						Name:   "app_backend",
						Driver: "overlay",
					},
				},
			},
		},
		networkReconciler: newNetworkReconciler(manager),
		stateStore:        store,
	}

	err := c.syncNetworks(context.Background(), "commit-4")
	require.NoError(t, err, "sync networks")
	assert.Equal(t, 0, manager.getCalls, "expected no docker inspect when state already synced")
	assert.Equal(t, 0, manager.createCalls, "expected no docker create when state already synced")

	state := store.Get()
	networkState := state.Networks["app_backend"]
	assert.Equal(t, "success", networkState.LastStatus, "expected previous successful status preserved")
	assert.Equal(t, "commit-4", networkState.LastCommit, "expected previous commit preserved")
}
