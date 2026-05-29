package handlers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/node"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestHandlerGetServiceRealtime_MapsNodeHostname(t *testing.T) {
	t.Parallel()

	nodeStore, err := swarmnode.NewNodeStore(filepath.Join(t.TempDir(), "nodes.json"))
	require.NoError(t, err)
	require.NoError(t, nodeStore.Replace([]swarm.Node{
		{
			ID:       "node-1",
			Hostname: "worker-1",
		},
	}))

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{
		serviceInspector: serviceInspector,
		nodes:            nodeStore,
	}

	serviceInspector.EXPECT().
		ListTasks(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return([]swarm.ServiceTask{
			{
				ID:           "task-1",
				Node:         "node-1",
				CreatedAt:    time.Date(2026, time.May, 29, 9, 0, 0, 0, time.UTC),
				UpdatedAt:    time.Date(2026, time.May, 29, 9, 1, 0, 0, time.UTC),
				CurrentState: "running",
			},
		}, nil)

	resp, err := h.GetServiceRealtime(context.Background(), generated.GetServiceRealtimeParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.Len(t, resp.Tasks, 1)

	nodeName, ok := resp.Tasks[0].NodeName.Get()
	require.True(t, ok)
	assert.Equal(t, "worker-1", nodeName)
	assert.Equal(t, "node-1", resp.Tasks[0].Node)
	createdAt, ok := resp.Tasks[0].CreatedAt.Get()
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.May, 29, 9, 0, 0, 0, time.UTC), createdAt)
	updatedAt, ok := resp.Tasks[0].UpdatedAt.Get()
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.May, 29, 9, 1, 0, 0, time.UTC), updatedAt)
}

func TestHandlerGetServiceRealtime_LeavesNodeNameEmptyIfNodeIsUnknown(t *testing.T) {
	t.Parallel()

	nodeStore, err := swarmnode.NewNodeStore(filepath.Join(t.TempDir(), "nodes.json"))
	require.NoError(t, err)
	require.NoError(t, nodeStore.Replace([]swarm.Node{
		{
			ID:       "node-2",
			Hostname: "worker-2",
		},
	}))

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{
		serviceInspector: serviceInspector,
		nodes:            nodeStore,
	}

	serviceInspector.EXPECT().
		ListTasks(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return([]swarm.ServiceTask{
			{
				ID:           "task-1",
				Node:         "node-1",
				CurrentState: "running",
			},
		}, nil)

	resp, err := h.GetServiceRealtime(context.Background(), generated.GetServiceRealtimeParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.Len(t, resp.Tasks, 1)

	_, ok := resp.Tasks[0].NodeName.Get()
	assert.False(t, ok)
	assert.Equal(t, "node-1", resp.Tasks[0].Node)
}

func TestHandlerGetServiceRealtime_NotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{
		serviceInspector: serviceInspector,
	}

	serviceInspector.EXPECT().
		ListTasks(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return(nil, swarm.ErrServiceNotFound)

	_, err := h.GetServiceRealtime(context.Background(), generated.GetServiceRealtimeParams{
		Stack:   "payments",
		Service: "api",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 404, statusErr.code)
}
