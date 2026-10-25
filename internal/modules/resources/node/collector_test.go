package node

import (
	"context"
	"errors"
	"testing"
	"time"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

type collectorStep struct {
	action dockerevents.Action
	nodeID string
	// list is a nodes snapshot returned by inspector after the event; nil means list failure.
	list []swarm.Node
}

func TestCollectorRunPreservesNodeEventSemantics(t *testing.T) {
	ready := func(id, host string, status swarm.NodeManagerStatus) swarm.Node {
		return swarm.Node{ID: id, Hostname: host, Status: "ready", ManagerStatus: status}
	}
	down := func(id, host string) swarm.Node {
		return swarm.Node{ID: id, Hostname: host, Status: "down", ManagerStatus: swarm.NodeManagerStatusWorker}
	}

	tests := []struct {
		name string
		// stored is a snapshot persisted before the collector starts.
		stored []swarm.Node
		// initialList is the initial snapshot returned by Docker.
		initialList []swarm.Node
		// initialErr makes the initial snapshot refresh fail.
		initialErr bool
		steps      []collectorStep
		want       []events.Event
	}{
		{
			name:        "failed initial refresh preserves stored snapshot",
			stored:      []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			initialErr:  true,
			initialList: nil,
		},
		{
			name:        "node created emits only NodeJoined with enriched metadata",
			initialList: []swarm.Node{ready("m1", "manager", swarm.NodeManagerStatusLeader)},
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
			name:        "reachable manager is recognized as manager",
			initialList: nil,
			steps: []collectorStep{{
				action: dockerevents.ActionCreate,
				nodeID: "m2",
				list:   []swarm.Node{ready("m2", "manager-2", swarm.NodeManagerStatusReachable)},
			}},
			want: []events.Event{&events.NodeJoined{NodeID: "m2", NodeName: "manager-2", Role: "manager"}},
		},
		{
			name:        "unreachable manager is recognized as manager",
			initialList: nil,
			steps: []collectorStep{{
				action: dockerevents.ActionCreate,
				nodeID: "m3",
				list:   []swarm.Node{ready("m3", "manager-3", swarm.NodeManagerStatusUnreachable)},
			}},
			want: []events.Event{&events.NodeJoined{NodeID: "m3", NodeName: "manager-3", Role: "manager"}},
		},
		{
			name:        "node created with missing metadata dispatches node id only",
			initialList: nil,
			steps: []collectorStep{{
				action: dockerevents.ActionCreate,
				nodeID: "ghost",
				list:   []swarm.Node{},
			}},
			want: []events.Event{&events.NodeJoined{NodeID: "ghost"}},
		},
		{
			name:        "failed refresh does not commit an unverified node event",
			initialList: nil,
			steps:       []collectorStep{{action: dockerevents.ActionCreate, nodeID: "w9", list: nil}},
		},
		{
			name:        "new node appearing without create event is not inferred",
			initialList: []swarm.Node{},
			steps: []collectorStep{{
				action: dockerevents.ActionUpdate,
				nodeID: "w1",
				list:   []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			}},
		},
		{
			name:        "existing node becoming ready emits NodeConnected",
			initialList: []swarm.Node{down("w1", "worker-1")},
			steps: []collectorStep{{
				action: dockerevents.ActionUpdate,
				nodeID: "w1",
				list:   []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			}},
			want: []events.Event{&events.NodeConnected{NodeID: "w1", NodeName: "worker-1", Status: "ready"}},
		},
		{
			name:        "created node is not reported as connected and later becoming ready is",
			initialList: []swarm.Node{},
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
			name:        "ready node going down emits NodeDisconnected",
			initialList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			steps: []collectorStep{{
				action: dockerevents.ActionUpdate,
				nodeID: "w1",
				list:   []swarm.Node{down("w1", "worker-1")},
			}},
			want: []events.Event{&events.NodeDisconnected{NodeID: "w1", NodeName: "worker-1", Status: "down"}},
		},
		{
			name:        "removed ready node emits NodeDisconnected",
			initialList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
			steps: []collectorStep{{
				action: dockerevents.ActionRemove,
				nodeID: "w1",
				list:   []swarm.Node{},
			}},
			want: []events.Event{&events.NodeDisconnected{NodeID: "w1", NodeName: "worker-1", Status: "missing"}},
		},
		{
			name:        "initial snapshot does not emit events",
			stored:      nil,
			initialList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker), down("w2", "worker-2")},
		},
		{
			name:   "initial reconciliation reports observed disconnects without synthetic joins",
			stored: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker), ready("w2", "worker-2", swarm.NodeManagerStatusWorker)},
			initialList: []swarm.Node{
				down("w1", "worker-1"),
				ready("w3", "worker-3", swarm.NodeManagerStatusWorker),
			},
			want: []events.Event{
				&events.NodeDisconnected{NodeID: "w1", NodeName: "worker-1", Status: "down"},
				&events.NodeDisconnected{NodeID: "w2", NodeName: "worker-2", Status: "missing"},
			},
		},
		{
			name:        "repeated event without state change emits nothing",
			initialList: []swarm.Node{ready("w1", "worker-1", swarm.NodeManagerStatusWorker)},
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
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			inspector := swarm.NewMockNodeManager(ctrl)
			dispatcherMock := dispatcher.NewMockDispatcher(ctrl)

			db, err := storage.Open(context.Background(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			store := NewSQLStore(db)
			require.NoError(t, store.ReplaceSnapshot(context.Background(), test.stored))

			eventsCh := make(chan swarm.NodeEvent, len(test.steps))
			resyncCh := make(chan swarm.ResyncEvent)
			for _, step := range test.steps {
				eventsCh <- swarm.NodeEvent{
					NodeID: step.nodeID,
					Action: swarm.EventAction(step.action),
				}
			}
			close(eventsCh)

			if test.initialErr {
				inspector.EXPECT().List(gomock.Any()).Return(nil, errors.New("list failed"))
				collector := NewNodeCollector(inspector, store, dispatcherMock, db, swarm.EventSubscription[swarm.NodeEvent]{
					Events: eventsCh,
					Resync: resyncCh,
				})
				require.Error(t, collector.Run(context.Background()))
				assert.Equal(t, test.stored, readNodes(t, store))
				return
			}

			lists := [][]swarm.Node{test.initialList}
			for _, step := range test.steps {
				lists = append(lists, step.list)
			}
			call := 0
			inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
				current := lists[call]
				call++
				if current == nil && call > 1 && test.steps[call-2].list == nil {
					return nil, errors.New("list failed")
				}
				return current, nil
			}).Times(len(lists))

			var got []events.Event
			dispatcherMock.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					got = append(got, event)
					return nil
				},
			).AnyTimes()

			collector := NewNodeCollector(inspector, store, dispatcherMock, db, swarm.EventSubscription[swarm.NodeEvent]{
				Events: eventsCh,
				Resync: resyncCh,
			})
			if len(test.steps) == 1 && test.steps[0].list == nil {
				// A failed individual event is retained by Run for retry; exercise
				// its initial failure directly without waiting for a retry loop.
				require.NoError(t, collector.refresh(context.Background()))
				require.Error(t, collector.handleEvent(context.Background(), swarm.NodeEvent{
					NodeID: test.steps[0].nodeID, Action: swarm.EventAction(test.steps[0].action),
				}))
			} else {
				require.Error(t, collector.Run(context.Background()))
			}

			assert.Equal(t, test.want, got)
			assert.Len(t, readNodes(t, store), len(lastNonNil(lists)))
		})
	}
}

func TestCollectorRunReconcilesSnapshotOnResync(t *testing.T) {
	collector, inspector, dispatcherMock, store, eventsCh, resyncCh := newCollectorWithSubscriptionForTest(t, []swarm.Node{{
		ID: "w1", Hostname: "worker-1", Status: "ready",
	}})

	listCalls := make(chan struct{}, 2)
	gomock.InOrder(
		inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
			listCalls <- struct{}{}
			return []swarm.Node{{ID: "w1", Hostname: "worker-1", Status: "ready"}}, nil
		}),
		inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
			listCalls <- struct{}{}
			return []swarm.Node{
				{ID: "w1", Hostname: "worker-1", Status: "down"},
				{ID: "w2", Hostname: "worker-2", Status: "ready"},
			}, nil
		}),
	)
	dispatcherMock.EXPECT().Publish(gomock.Any(), &events.NodeDisconnected{
		NodeID: "w1", NodeName: "worker-1", Status: "down",
	}).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	requireSignal(t, listCalls)
	resyncCh <- swarm.ResyncEvent{Reason: swarm.ResyncReasonReconnect}
	requireSignal(t, listCalls)
	require.Eventually(t, func() bool {
		nodes, err := store.ReadAll(context.Background())
		return err == nil && len(nodes) == 2
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	assert.Len(t, readNodes(t, store), 2)
	close(eventsCh)
}

func TestCollectorRunStartsAndStopsWithContext(t *testing.T) {
	collector, inspector, dispatcherMock, store, eventsCh, _ := newCollectorWithSubscriptionForTest(t, nil)
	refreshed := make(chan struct{}, 1)
	inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
		refreshed <- struct{}{}
		return []swarm.Node{{ID: "w1", Hostname: "worker-1", Status: "ready"}}, nil
	})
	dispatcherMock.EXPECT().Publish(gomock.Any(), gomock.Any()).Times(0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	requireSignal(t, refreshed)
	require.Eventually(t, func() bool {
		nodes, err := store.ReadAll(context.Background())
		return err == nil && len(nodes) == 1
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	assert.Len(t, readNodes(t, store), 1)
	close(eventsCh)
}

func newCollectorForTest(
	t *testing.T,
	stored []swarm.Node,
) (*Collector, *swarm.MockNodeManager, *dispatcher.MockDispatcher, *SQLStore) {
	t.Helper()
	collector, inspector, dispatcherMock, store, _, _ := newCollectorWithSubscriptionForTest(t, stored)
	return collector, inspector, dispatcherMock, store
}

func newCollectorWithSubscriptionForTest(
	t *testing.T,
	stored []swarm.Node,
) (*Collector, *swarm.MockNodeManager, *dispatcher.MockDispatcher, *SQLStore, chan swarm.NodeEvent, chan swarm.ResyncEvent) {
	t.Helper()

	ctrl := gomock.NewController(t)
	inspector := swarm.NewMockNodeManager(ctrl)
	dispatcherMock := dispatcher.NewMockDispatcher(ctrl)

	db, err := storage.Open(context.Background(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)
	require.NoError(t, store.ReplaceSnapshot(context.Background(), stored))

	eventsCh := make(chan swarm.NodeEvent)
	resyncCh := make(chan swarm.ResyncEvent)
	collector := NewNodeCollector(inspector, store, dispatcherMock, db, swarm.EventSubscription[swarm.NodeEvent]{
		Events: eventsCh,
		Resync: resyncCh,
	})
	return collector, inspector, dispatcherMock, store, eventsCh, resyncCh
}


func TestCollectorRunRetriesFailedResyncWithoutNewDockerEvents(t *testing.T) {
	collector, inspector, publisher, store, _, resyncCh := newCollectorWithSubscriptionForTest(t, []swarm.Node{{
		ID: "w1", Hostname: "worker-1", Status: "ready",
	}})
	collector.retryDelay = 10 * time.Millisecond

	initial := []swarm.Node{{ID: "w1", Hostname: "worker-1", Status: "ready"}}
	recovered := []swarm.Node{{ID: "w1", Hostname: "worker-1", Status: "down"}}
	listCalls := make(chan struct{}, 3)
	gomock.InOrder(
		inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
			listCalls <- struct{}{}
			return initial, nil
		}),
		inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
			listCalls <- struct{}{}
			return nil, errors.New("docker temporarily unavailable")
		}),
		inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
			listCalls <- struct{}{}
			return recovered, nil
		}),
	)
	publisher.EXPECT().Publish(gomock.Any(), &events.NodeDisconnected{
		NodeID: "w1", NodeName: "worker-1", Status: "down",
	}).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	requireSignal(t, listCalls)
	resyncCh <- swarm.ResyncEvent{Reason: swarm.ResyncReasonReconnect}
	requireSignal(t, listCalls)
	requireSignal(t, listCalls)
	require.Eventually(t, func() bool {
		nodes, err := store.ReadAll(context.Background())
		return err == nil && len(nodes) == 1 && nodes[0].Status == "down"
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestCollectorRunRetriesFailedInitialSnapshotWithoutDockerEvents(t *testing.T) {
	collector, inspector, publisher, store, _, _ := newCollectorWithSubscriptionForTest(t, nil)
	collector.retryDelay = 10 * time.Millisecond
	inspector.EXPECT().List(gomock.Any()).Return(nil, errors.New("docker unavailable"))
	inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{{
		ID: "w1", Hostname: "worker-1", Status: "ready",
	}}, nil)
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Times(0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	require.Eventually(t, func() bool {
		nodes, err := store.ReadAll(context.Background())
		return err == nil && len(nodes) == 1 && nodes[0].ID == "w1"
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestCollectorReconcileHandlesQueuedCreateBeforeSnapshot(t *testing.T) {
	base, inspector, publisher, store, _, _ := newCollectorWithSubscriptionForTest(t, nil)
	eventsCh := make(chan swarm.NodeEvent, 1)
	eventsCh <- swarm.NodeEvent{NodeID: "w1", Action: swarm.EventActionCreate}
	collector := NewNodeCollector(inspector, store, publisher, base.db, swarm.EventSubscription[swarm.NodeEvent]{
		Events: eventsCh,
		Resync: make(chan swarm.ResyncEvent, 1),
	})
	node := swarm.Node{ID: "w1", Hostname: "worker-1", Status: "ready", ManagerStatus: swarm.NodeManagerStatusWorker}
	inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{node}, nil).Times(2)
	publisher.EXPECT().Publish(gomock.Any(), &events.NodeJoined{
		NodeID: "w1", NodeName: "worker-1", Role: "worker",
	}).Return(nil).Times(1)

	require.NoError(t, collector.reconcileAfterResync(context.Background()))
	assert.Equal(t, []swarm.Node{node}, readNodes(t, store))
}


func TestCollectorResyncRetainsJoinDuringSnapshot(t *testing.T) {
	collector, inspector, publisher, store, _, _ := newCollectorWithSubscriptionForTest(t, nil)
	node := swarm.Node{ID: "new", Hostname: "worker", Status: "ready", ManagerStatus: swarm.NodeManagerStatusWorker}
	inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{node}, nil).Times(3)
	publisher.EXPECT().Publish(gomock.Any(), &events.NodeJoined{
		NodeID: "new", NodeName: "worker", Role: "worker",
	}).Return(nil).Times(1)

	// The snapshot sees the node before its Docker Create event is delivered.
	require.NoError(t, collector.reconcileAfterResync(context.Background()))
	require.NoError(t, collector.handleEvent(context.Background(), swarm.NodeEvent{
		NodeID: "new", Action: swarm.EventActionCreate,
	}))
	assert.Equal(t, []swarm.Node{node}, readNodes(t, store))
	// A duplicate Create should never publish another join.
	require.NoError(t, collector.handleEvent(context.Background(), swarm.NodeEvent{
		NodeID: "new", Action: swarm.EventActionCreate,
	}))
}

func TestCreationFactJoinCandidateTTL(t *testing.T) {
	for _, test := range []struct {
		name string
		age  time.Duration
		wantJoined bool
	}{
		{name: "recent", age: observedJoinTTL / 2, wantJoined: true},
		{name: "expired", age: observedJoinTTL + time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector, _, _, store := newCollectorForTest(t, nil)
			ctx := context.Background()
			node := swarm.Node{ID: "node-1", Hostname: "worker-1", Status: "down"}
			require.NoError(t, store.ReplaceSnapshot(ctx, []swarm.Node{node}))
			collector.joinCandidates[node.ID] = time.Now().Add(-test.age)
			fact, err := collector.creationFact(ctx, node.ID, nodesByID([]swarm.Node{node}), nodesByID([]swarm.Node{node}))
			require.NoError(t, err)
			if test.wantJoined {
				assert.Equal(t, &events.NodeJoined{NodeID: node.ID, NodeName: node.Hostname}, fact)
			} else {
				assert.Nil(t, fact, "expired candidate must not emit NodeJoined")
			}
		})
	}
}

func TestCollectorExpiredJoinCandidateDoesNotEmitJoined(t *testing.T) {
	collector, inspector, publisher, store, _, _ := newCollectorWithSubscriptionForTest(t, nil)
	node := swarm.Node{ID: "known", Hostname: "worker", Status: "ready", ManagerStatus: swarm.NodeManagerStatusWorker}
	require.NoError(t, store.ReplaceSnapshot(context.Background(), []swarm.Node{node}))
	collector.joinCandidates["known"] = time.Now().Add(-observedJoinTTL - time.Second)
	inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{node}, nil)
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Times(0)

	require.NoError(t, collector.handleEvent(context.Background(), swarm.NodeEvent{
		NodeID: "known", Action: swarm.EventActionCreate,
	}))
	_, present := collector.joinCandidates["known"]
	assert.False(t, present, "expired candidate should be removed during lookup")
}

func TestCollectorRunRetriesFailedNodeEventWithoutFurtherEvents(t *testing.T) {
	collector, inspector, publisher, store, eventCh, _ := newCollectorWithSubscriptionForTest(t, nil)
	collector.retryDelay = 10 * time.Millisecond
	node := swarm.Node{ID: "new", Hostname: "worker", Status: "ready", ManagerStatus: swarm.NodeManagerStatusWorker}
	initial := make(chan struct{}, 1)
	gomock.InOrder(
		inspector.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]swarm.Node, error) {
			initial <- struct{}{}
			return nil, nil
		}),
		inspector.EXPECT().List(gomock.Any()).Return(nil, errors.New("temporary docker failure")),
		inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{node}, nil),
		inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{node}, nil),
	)
	publisher.EXPECT().Publish(gomock.Any(), &events.NodeJoined{
		NodeID: "new", NodeName: "worker", Role: "worker",
	}).Return(nil).Times(1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	requireSignal(t, initial)
	eventCh <- swarm.NodeEvent{NodeID: "new", Action: swarm.EventActionCreate}
	require.Eventually(t, func() bool {
		nodes, err := store.ReadAll(context.Background())
		return err == nil && len(nodes) == 1 && nodes[0].ID == "new"
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func lastNonNil(lists [][]swarm.Node) []swarm.Node {
	for index := len(lists) - 1; index >= 0; index-- {
		if lists[index] != nil {
			return lists[index]
		}
	}

	return nil
}

func requireSignal(t *testing.T, signals <-chan struct{}) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(time.Second):
		require.Fail(t, "timed out waiting for signal")
	}
}

func readNodes(t *testing.T, store *SQLStore) []swarm.Node {
	t.Helper()
	nodes, err := store.ReadAll(context.Background())
	require.NoError(t, err)
	return nodes
}
