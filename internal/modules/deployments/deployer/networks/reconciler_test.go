package networks

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileCreatesAndReusesComposeNetworks(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		current  swarm.Network
		getError error
		create   bool
	}{
		{name: "creates missing network", getError: swarm.ErrNetworkNotFound, create: true},
		{name: "reuses compatible network", current: compatibleNetwork("demo_default")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := swarm.NewMockNetworkManager(gomock.NewController(t))
			planned := managedPlan("default", "demo_default")
			first := manager.EXPECT().Get(gomock.Any(), "demo_default").Return(testCase.current, testCase.getError)
			if testCase.create {
				manager.EXPECT().Create(gomock.Any(), planned.Spec).After(first).Return("network-id", nil)
				manager.EXPECT().Get(gomock.Any(), "demo_default").Return(compatibleNetwork("demo_default"), nil)
			}
			err := New(manager).Reconcile(t.Context(), "demo", []converter.NetworkSpec{planned})
			require.NoError(t, err)
		})
	}
}

func TestReconcileExternalNetworkMustExistWithoutCreation(t *testing.T) {
	manager := swarm.NewMockNetworkManager(gomock.NewController(t))
	manager.EXPECT().Get(gomock.Any(), "edge").Return(swarm.Network{}, swarm.ErrNetworkNotFound)
	err := New(manager).Reconcile(t.Context(), "demo", []converter.NetworkSpec{{
		Alias: "public", External: true, Spec: swarm.CreateNetworkRequest{Name: "edge"},
	}})
	require.Error(t, err)
	assert.ErrorContains(t, err, "external network edge does not exist")
}

func TestReconcileRejectsOwnershipAndConfigurationConflicts(t *testing.T) {
	for _, testCase := range []struct {
		name, want string
		mutate     func(*swarm.Network)
	}{
		{name: "ownership", want: "not owned", mutate: func(network *swarm.Network) {
			network.Labels[labelsdict.StackNamespace] = "other"
		}},
		{name: "driver", want: "has driver", mutate: func(network *swarm.Network) { network.Driver = "bridge" }},
		{name: "option", want: "driver option", mutate: func(network *swarm.Network) {
			delete(network.Options, "encrypted")
		}},
		{name: "internal", want: "has internal", mutate: func(network *swarm.Network) { network.Internal = true }},
		{name: "attachable", want: "has attachable", mutate: func(network *swarm.Network) {
			network.Attachable = false
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := swarm.NewMockNetworkManager(gomock.NewController(t))
			current := compatibleNetwork("demo_backend")
			testCase.mutate(&current)
			manager.EXPECT().Get(gomock.Any(), "demo_backend").Return(current, nil)
			err := New(manager).Reconcile(t.Context(), "demo", []converter.NetworkSpec{managedPlan("backend", "demo_backend")})
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestReconcileReturnsNetworkAPIFailure(t *testing.T) {
	manager := swarm.NewMockNetworkManager(gomock.NewController(t))
	manager.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{}, errors.New("daemon unavailable"))

	err := New(manager).Reconcile(t.Context(), "demo", []converter.NetworkSpec{
		managedPlan("default", "demo_default"),
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "inspect network demo_default")
}

func TestReconcileAcceptsConcurrentCreationAfterCreateFailure(t *testing.T) {
	manager := swarm.NewMockNetworkManager(gomock.NewController(t))
	planned := managedPlan("default", "demo_default")
	first := manager.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{}, swarm.ErrNetworkNotFound)
	manager.EXPECT().Create(gomock.Any(), planned.Spec).After(first).Return("", errors.New("name conflict"))
	manager.EXPECT().Get(gomock.Any(), "demo_default").Return(compatibleNetwork("demo_default"), nil)
	require.NoError(t, New(manager).Reconcile(context.Background(), "demo", []converter.NetworkSpec{planned}))
}

func managedPlan(alias, name string) converter.NetworkSpec {
	return converter.NetworkSpec{Alias: alias, Spec: swarm.CreateNetworkRequest{
		Name: name, Driver: "overlay", Attachable: true,
		Labels:  map[string]string{labelsdict.StackNamespace: "demo", "team": "platform"},
		Options: map[string]string{"encrypted": ""},
	}}
}

func compatibleNetwork(name string) swarm.Network {
	return swarm.Network{
		ID: "network-id", Name: name, Scope: "swarm", Driver: "overlay", Attachable: true,
		Labels:  map[string]string{labelsdict.StackNamespace: "demo", "team": "platform"},
		Options: map[string]string{"encrypted": "", "driver-default": "value"},
	}
}
