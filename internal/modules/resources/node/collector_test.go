package node

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

type collectorStep struct {
	action dockerevents.Action
	nodeID string
	// list is a nodes snapshot returned by inspector after the event; nil means list failure.
	list []swarm.Node
}

func TestCollector_WatchOnce(t *testing.T) {
	ready := func(id, host string, status swarm.NodeManagerStatus) swarm.Node {
		return swarm.Node{ID: id, Hostname: host, Status: "ready", ManagerStatus: status}
	}
	down := func(id, host string) swarm.Node {
		return swarm.Node{ID: id, Hostname: host, Status: "down", ManagerStatus: swarm.NodeManagerStatusWorker}
	}

	tests := []struct {
		name string
		// stored is a snapshot persisted before watch starts.
		stored []swarm.Node
		// subscribeList is a snapshot returned by refresh right after subscribing.
		subscribeList []swarm.Node
		// subscribeErr makes refresh right after subscribing fail.
		subscribeErr bool
		steps        []collectorStep
		want         []events.Event
	}{
		{
			name:          "failed refresh after subscribe returns error and skips events",
			stored:        []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			subscribeErr:  true,
			subscribeList: nil,
			want:          nil,
		},
		{
			name:          "node created emits only nodeJoined with enriched metadata",
			subscribeList: []swarm.Node{ready("m1", "manager", swarm.NodeManagerStatusLeader)},
			steps: []collectorStep{{
				action: dockerevents.ActionCreate,
				nodeID: "w1",
				list: []swarm.Node{
					ready("m1", "manager", swarm.NodeManagerStatusLeader),
					ready("w1", "worker-1", swarm.NodeManagerStatusWorker),
				},
			}},
			want: []events.Event{&events.NodeJoined{NodeID: "w1", NodeName: "worker-1", Role: "worker"}},
		},
		{
			name:          "reachable manager is recognized as manager",
			subscribeList: nil,
			steps: []collectorStep{{
				action: dockerevents.ActionCreate,
				nodeID: "m2",
				list:   []swarm.Node{ready("m2", "manager-2", swarm.NodeManagerStatusReachable)},
			}},
			want: []events.Event{&events.NodeJoined{NodeID: "m2", NodeName: "manager-2", Role: "manager"}},
		},
		{
			name:          "unreachable manager is recognized as manager",
			subscribeList: nil,
			steps: []collectorStep{{
				action: dockerevents.ActionCreate,
				nodeID: "m3",
				list:   []swarm.Node{ready("m3", "manager-3", swarm.NodeManagerStatusUnreachable)},
			}},
			want: []events.Event{&events.NodeJoined{NodeID: "m3", NodeName: "manager-3", Role: "manager"}},
		},
		{
			name:          "node created with missing metadata dispatches node id only",
			subscribeList: nil,
			steps: []collectorStep{{
				action: dockerevents.ActionCreate,
				nodeID: "ghost",
				list:   []swarm.Node{},
			}},
			want: []events.Event{&events.NodeJoined{NodeID: "ghost"}},
		},
		{
			name:          "node created dispatches joined even when refresh fails",
			subscribeList: nil,
			steps:         []collectorStep{{action: dockerevents.ActionCreate, nodeID: "w9", list: nil}},
			want:          []events.Event{&events.NodeJoined{NodeID: "w9"}},
		},
		{
			name:          "new node appearing without create event is not inferred",
			subscribeList: []swarm.Node{},
			steps: []collectorStep{{
				action: dockerevents.ActionUpdate,
				nodeID: "w1",
				list:   []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			}},
			want: nil,
		},
		{
			name:          "existing node becoming ready emits nodeConnected",
			subscribeList: []swarm.Node{down("w1", "worker-1")},
			steps: []collectorStep{{
				action: dockerevents.ActionUpdate,
				nodeID: "w1",
				list:   []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			}},
			want: []events.Event{&events.NodeConnected{NodeID: "w1", NodeName: "worker-1", Status: "ready"}},
		},
		{
			name:          "created node is not reported as connected and later becoming ready is",
			subscribeList: []swarm.Node{},
			steps: []collectorStep{
				{
					action: dockerevents.ActionCreate,
					nodeID: "w1",
					list:   []swarm.Node{down("w1", "worker-1")},
				},
				{
					action: dockerevents.ActionUpdate,
					nodeID: "w1",
					list:   []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
				},
			},
			want: []events.Event{
				&events.NodeJoined{NodeID: "w1", NodeName: "worker-1", Role: "worker"},
				&events.NodeConnected{NodeID: "w1", NodeName: "worker-1", Status: "ready"},
			},
		},
		{
			name:          "ready node going down emits nodeDisconnected",
			subscribeList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			steps: []collectorStep{{
				action: dockerevents.ActionUpdate,
				nodeID: "w1",
				list:   []swarm.Node{down("w1", "worker-1")},
			}},
			want: []events.Event{&events.NodeDisconnected{NodeID: "w1", NodeName: "worker-1", Status: "down"}},
		},
		{
			name:          "removed ready node emits nodeDisconnected",
			subscribeList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			steps: []collectorStep{{
				action: dockerevents.ActionRemove,
				nodeID: "w1",
				list:   []swarm.Node{},
			}},
			want: []events.Event{&events.NodeDisconnected{NodeID: "w1", NodeName: "worker-1", Status: "missing"}},
		},
		{
			name:          "initial snapshot does not emit events",
			stored:        nil,
			subscribeList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker), down("w2", "worker-2")},
			steps:         nil,
			want:          nil,
		},
		{
			name:          "reconnection refreshes snapshot without synthetic events",
			stored:        []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker), ready("w2", "worker-2", swarm.NodeManagerStatusWorker)},
			subscribeList: []swarm.Node{down("w1", "worker-1"), ready("w3", "worker-3", swarm.NodeManagerStatusWorker)},
			steps:         nil,
			want:          nil,
		},
		{
			name:          "repeated event without state change emits nothing",
			subscribeList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			steps: []collectorStep{
				{
					action: dockerevents.ActionUpdate,
					nodeID: "w1",
					list:   []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
				},
				{
					action: dockerevents.ActionUpdate,
					nodeID: "w1",
					list:   []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			inspector := swarm.NewMockNodeManager(ctrl)
			disp := dispatcher.NewMockDispatcher(ctrl)

			store, err := NewNodeStore(filepath.Join(t.TempDir(), "nodes.json"))
			require.NoError(t, err)
			require.NoError(t, store.Replace(tt.stored))

			eventsCh := make(chan dockerevents.Message, len(tt.steps))
			errorsCh := make(chan error)
			inspector.EXPECT().Watch(gomock.Any()).Return((<-chan dockerevents.Message)(eventsCh), (<-chan error)(errorsCh), nil)

			if tt.subscribeErr {
				inspector.EXPECT().List(gomock.Any()).Return(nil, errors.New("list failed"))
				collector := NewNodeCollector(inspector, store, disp)
				err = collector.watchOnce(context.Background())
				require.Error(t, err, "failed refresh after subscribe must return error")
				assert.Len(t, store.List(), len(tt.stored), "snapshot must stay untouched")
				return
			}

			lists := [][]swarm.Node{tt.subscribeList}
			for _, step := range tt.steps {
				lists = append(lists, step.list)
			}
			call := 0
			inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
				current := lists[call]
				call++
				if current == nil && call > 1 && tt.steps[call-2].list == nil {
					return nil, errors.New("list failed")
				}
				return current, nil
			}).Times(len(lists))

			var got []events.Event
			disp.EXPECT().Dispatch(gomock.Any(), gomock.Any()).Do(func(_ context.Context, event events.Event) {
				got = append(got, event)
			}).AnyTimes()

			for _, step := range tt.steps {
				eventsCh <- dockerevents.Message{
					Type:   dockerevents.NodeEventType,
					Action: step.action,
					Actor:  dockerevents.Actor{ID: step.nodeID},
				}
			}
			close(eventsCh)

			collector := NewNodeCollector(inspector, store, disp)
			err = collector.watchOnce(context.Background())
			require.Error(t, err, "closed events channel must end watch")

			assert.Equal(t, tt.want, got)
			assert.Len(t, store.List(), len(lastNonNil(lists)))
		})
	}
}

func lastNonNil(lists [][]swarm.Node) []swarm.Node {
	for i := len(lists) - 1; i >= 0; i-- {
		if lists[i] != nil {
			return lists[i]
		}
	}

	return nil
}

func newCollectorForTest(t *testing.T, stored []swarm.Node) (*Collector, *swarm.MockNodeManager, *dispatcher.MockDispatcher, *Store) {
	t.Helper()

	ctrl := gomock.NewController(t)
	inspector := swarm.NewMockNodeManager(ctrl)
	disp := dispatcher.NewMockDispatcher(ctrl)

	store, err := NewNodeStore(filepath.Join(t.TempDir(), "nodes.json"))
	require.NoError(t, err)
	require.NoError(t, store.Replace(stored))

	return NewNodeCollector(inspector, store, disp), inspector, disp, store
}

func TestCollector_WatchOnce_CancelsSubscription(t *testing.T) {
	tests := []struct {
		name  string
		setup func(inspector *swarm.MockNodeManager, eventsCh chan dockerevents.Message, errorsCh chan error)
	}{
		{
			name: "failed snapshot refresh",
			setup: func(inspector *swarm.MockNodeManager, _ chan dockerevents.Message, _ chan error) {
				inspector.EXPECT().List(gomock.Any()).Return(nil, errors.New("list failed"))
			},
		},
		{
			name: "stream error",
			setup: func(inspector *swarm.MockNodeManager, _ chan dockerevents.Message, errorsCh chan error) {
				inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{}, nil)
				errorsCh <- errors.New("stream broke")
			},
		},
		{
			name: "events channel closed",
			setup: func(inspector *swarm.MockNodeManager, eventsCh chan dockerevents.Message, _ chan error) {
				inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{}, nil)
				close(eventsCh)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector, inspector, _, _ := newCollectorForTest(t, nil)

			eventsCh := make(chan dockerevents.Message, 1)
			errorsCh := make(chan error, 1)
			var watchCtx context.Context
			inspector.EXPECT().Watch(gomock.Any()).DoAndReturn(
				func(ctx context.Context) (<-chan dockerevents.Message, <-chan error, error) {
					watchCtx = ctx
					return eventsCh, errorsCh, nil
				})
			tt.setup(inspector, eventsCh, errorsCh)

			err := collector.watchOnce(context.Background())
			require.Error(t, err)
			require.NotNil(t, watchCtx)
			assert.Error(t, watchCtx.Err(), "subscription context must be cancelled on exit")
		})
	}
}

func TestCollector_WatchOnce_InitialSyncWhenWatchFails(t *testing.T) {
	collector, inspector, disp, store := newCollectorForTest(t, nil)
	disp.EXPECT().Dispatch(gomock.Any(), gomock.Any()).Times(0)

	var watchCtx context.Context
	inspector.EXPECT().Watch(gomock.Any()).DoAndReturn(
		func(ctx context.Context) (<-chan dockerevents.Message, <-chan error, error) {
			watchCtx = ctx
			return nil, nil, errors.New("subscribe failed")
		}).Times(2)
	inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{
		{ID: "w1", Hostname: "worker-1", Status: "ready"},
	}, nil).Times(1)

	require.Error(t, collector.watchOnce(context.Background()))
	assert.Len(t, store.List(), 1, "initial snapshot must be loaded even if Watch fails")
	assert.Error(t, watchCtx.Err(), "subscription context must be cancelled")

	// Already synced: a repeated Watch failure must not call List again.
	require.Error(t, collector.watchOnce(context.Background()))
}

func TestCollector_WatchOnce_ReconnectRefreshesWithoutEvents(t *testing.T) {
	collector, inspector, disp, store := newCollectorForTest(t, nil)
	disp.EXPECT().Dispatch(gomock.Any(), gomock.Any()).Times(0)

	first := make(chan dockerevents.Message)
	close(first)
	second := make(chan dockerevents.Message)
	close(second)

	gomock.InOrder(
		inspector.EXPECT().Watch(gomock.Any()).Return((<-chan dockerevents.Message)(first), (<-chan error)(make(chan error)), nil),
		inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{
			{ID: "w1", Hostname: "worker-1", Status: "ready"},
		}, nil),
		inspector.EXPECT().Watch(gomock.Any()).Return((<-chan dockerevents.Message)(second), (<-chan error)(make(chan error)), nil),
		// While disconnected: w1 went down, w2 appeared. Neither may produce events.
		inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{
			{ID: "w1", Hostname: "worker-1", Status: "down"},
			{ID: "w2", Hostname: "worker-2", Status: "ready"},
		}, nil),
	)

	require.Error(t, collector.watchOnce(context.Background()))
	require.Error(t, collector.watchOnce(context.Background()))
	assert.Len(t, store.List(), 2, "snapshot must be refreshed after reconnect")
}
