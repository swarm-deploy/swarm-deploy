package networkloop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcilerReconcileCreatesManagedNetwork(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockNetworkManager(ctrl)
	eventDispatcher := dispatcher.NewMockDispatcher(ctrl)

	reconciler := New(manager, eventDispatcher)
	var createReq *swarm.CreateNetworkRequest
	var createdEvent *events.NetworkCreated
	manager.EXPECT().
		Get(gomock.Any(), "app_backend").
		Return(swarm.Network{}, swarm.ErrNetworkNotFound)
	manager.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req swarm.CreateNetworkRequest) (string, error) {
			createReq = &req
			return "created-id", nil
		})
	eventDispatcher.EXPECT().
		Dispatch(gomock.Any(), gomock.AssignableToTypeOf(&events.NetworkCreated{})).
		Do(func(_ context.Context, event events.Event) {
			createdEvent = event.(*events.NetworkCreated)
		})

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
	require.NotNil(t, createReq, "expected create request")
	assert.Equal(t, "app_backend", createReq.Name, "unexpected network name")
	assert.Equal(t, "overlay", createReq.Driver, "unexpected driver")
	assert.True(t, createReq.Attachable, "unexpected attachable flag")
	assert.Equal(
		t,
		labelsdict.NetworkManagedValue,
		createReq.Labels[labelsdict.NetworkManagedKey],
		"expected managed label",
	)
	assert.Equal(t, &events.NetworkCreated{
		NetworkName: "app_backend",
		NetworkID:   "created-id",
		Driver:      "overlay",
	}, createdEvent, "unexpected network-created event")
}

func TestReconcilerReconcileFailsWhenExistingNetworkIsNotManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockNetworkManager(ctrl)

	manager.EXPECT().
		Get(gomock.Any(), "app_backend").
		Return(swarm.Network{
			Name:   "app_backend",
			Driver: "overlay",
		}, nil)

	reconciler := New(manager, &dispatcher.NopDispatcher{})
	_, err := reconciler.Reconcile(context.Background(), config.NetworkSpec{
		Name:   "app_backend",
		Driver: "overlay",
	})

	require.Error(t, err, "expected ownership error")
	assert.Contains(t, err.Error(), "not managed by swarm-deploy", "unexpected error")
}

func TestReconcilerReconcileFailsOnManagedLabelOverride(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockNetworkManager(ctrl)

	reconciler := New(manager, &dispatcher.NopDispatcher{})
	_, err := reconciler.Reconcile(context.Background(), config.NetworkSpec{
		Name:   "app_backend",
		Driver: "overlay",
		Labels: map[string]string{
			labelsdict.NetworkManagedKey: "false",
		},
	})

	require.Error(t, err, "expected validation error")
	assert.Contains(t, err.Error(), `label "org.swarm-deploy.network.managed" must be "true"`, "unexpected error")
}

func TestReconcilerReconcileSkipsMatchingManagedNetwork(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockNetworkManager(ctrl)

	manager.EXPECT().
		Get(gomock.Any(), "app_backend").
		Return(swarm.Network{
			Name:       "app_backend",
			Driver:     "overlay",
			Attachable: true,
			Internal:   true,
			Labels: map[string]string{
				labelsdict.NetworkManagedKey: labelsdict.NetworkManagedValue,
				"team":                       "platform",
			},
			Options: map[string]string{
				"encrypted": "true",
				"mtu":       "1450",
			},
		}, nil)

	reconciler := New(manager, &dispatcher.NopDispatcher{})
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
}
