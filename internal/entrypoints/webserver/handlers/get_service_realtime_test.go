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
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/node"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestHandlerGetServiceRealtime_MapsNodeHostnameAndSortsTasksByCreatedAt(t *testing.T) {
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
				ID:           "task-oldest",
				Node:         "node-1",
				CreatedAt:    time.Date(2026, time.May, 29, 9, 0, 0, 0, time.UTC),
				UpdatedAt:    time.Date(2026, time.May, 29, 9, 1, 0, 0, time.UTC),
				CurrentState: swarm.TaskStateRunning,
			},
			{
				ID:           "task-newest",
				Node:         "node-1",
				CreatedAt:    time.Date(2026, time.May, 29, 11, 0, 0, 0, time.UTC),
				CurrentState: swarm.TaskStateRunning,
			},
			{
				ID:           "task-middle",
				Node:         "node-1",
				CreatedAt:    time.Date(2026, time.May, 29, 10, 0, 0, 0, time.UTC),
				CurrentState: swarm.TaskStateRunning,
			},
			{
				ID:           "task-stale-failed",
				Node:         "node-1",
				CreatedAt:    time.Date(2026, time.May, 29, 12, 0, 0, 0, time.UTC),
				UpdatedAt:    time.Now().Add(-13 * time.Hour),
				CurrentState: swarm.TaskStateFailed,
			},
			{
				ID:           "task-recent-shutdown",
				Node:         "node-1",
				CreatedAt:    time.Date(2026, time.May, 29, 13, 0, 0, 0, time.UTC),
				UpdatedAt:    time.Now(),
				CurrentState: swarm.TaskStateShutdown,
			},
		}, nil)

	resp, err := h.GetServiceRealtime(context.Background(), generated.GetServiceRealtimeParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.Len(t, resp.Tasks, 3)
	assert.Equal(t, []string{"task-newest", "task-middle", "task-oldest"}, []string{
		resp.Tasks[0].ID,
		resp.Tasks[1].ID,
		resp.Tasks[2].ID,
	})

	nodeName, ok := resp.Tasks[0].NodeName.Get()
	require.True(t, ok)
	assert.Equal(t, "worker-1", nodeName)
	assert.Equal(t, "node-1", resp.Tasks[0].Node)
	createdAt, ok := resp.Tasks[0].CreatedAt.Get()
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.May, 29, 11, 0, 0, 0, time.UTC), createdAt)
	updatedAt, ok := resp.Tasks[2].UpdatedAt.Get()
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
				CurrentState: swarm.TaskStateRunning,
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

func TestFilterServiceRealtimeTasks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-staleTerminalTaskAge)

	tests := []struct {
		name     string
		tasks    []swarm.ServiceTask
		expected []swarm.ServiceTask
	}{
		{
			name: "removes shutdown tasks regardless of update time",
			tasks: []swarm.ServiceTask{
				{ID: "old", CurrentState: swarm.TaskStateShutdown, UpdatedAt: cutoff.Add(-time.Second)},
				{ID: "recent", CurrentState: swarm.TaskStateShutdown, UpdatedAt: cutoff.Add(time.Hour)},
			},
			expected: []swarm.ServiceTask{},
		},
		{
			name: "removes stale failed and rejected tasks",
			tasks: []swarm.ServiceTask{
				{ID: "failed", CurrentState: swarm.TaskStateFailed, UpdatedAt: cutoff.Add(-time.Hour)},
				{ID: "rejected", CurrentState: swarm.TaskStateRejected, UpdatedAt: cutoff.Add(-24 * time.Hour)},
			},
			expected: []swarm.ServiceTask{},
		},
		{
			name: "keeps recent and boundary terminal tasks",
			tasks: []swarm.ServiceTask{
				{ID: "recent", CurrentState: swarm.TaskStateFailed, UpdatedAt: cutoff.Add(time.Second)},
				{ID: "boundary", CurrentState: swarm.TaskStateRejected, UpdatedAt: cutoff},
			},
			expected: []swarm.ServiceTask{
				{ID: "recent", CurrentState: swarm.TaskStateFailed, UpdatedAt: cutoff.Add(time.Second)},
				{ID: "boundary", CurrentState: swarm.TaskStateRejected, UpdatedAt: cutoff},
			},
		},
		{
			name: "keeps stale non-terminal tasks",
			tasks: []swarm.ServiceTask{
				{ID: "running", CurrentState: swarm.TaskStateRunning, UpdatedAt: cutoff.Add(-time.Hour)},
				{ID: "complete", CurrentState: swarm.TaskStateComplete, UpdatedAt: cutoff.Add(-time.Hour)},
			},
			expected: []swarm.ServiceTask{
				{ID: "running", CurrentState: swarm.TaskStateRunning, UpdatedAt: cutoff.Add(-time.Hour)},
				{ID: "complete", CurrentState: swarm.TaskStateComplete, UpdatedAt: cutoff.Add(-time.Hour)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := filterServiceRealtimeTasks(tt.tasks, cutoff)

			assert.Equal(t, tt.expected, actual)
		})
	}
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
