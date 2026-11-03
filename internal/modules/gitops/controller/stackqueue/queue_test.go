package stackqueue

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"go.uber.org/mock/gomock"
)

const testWait = 5 * time.Second

func TestQueueBoundsParallelism(t *testing.T) {
	tests := []struct {
		name        string
		parallelism int
		expected    int32
	}{
		{name: "sequential", parallelism: 1, expected: 1},
		{name: "below one is sequential", parallelism: 0, expected: 1},
		{name: "parallel", parallelism: 3, expected: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			reconciler := stackloop.NewMockStackReconciler(ctrl)
			var inFlight, peak atomic.Int32
			reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, stackloop.ReconciliationRequest) error {
					current := inFlight.Add(1)
					for {
						observed := peak.Load()
						if current <= observed || peak.CompareAndSwap(observed, current) {
							break
						}
					}
					time.Sleep(20 * time.Millisecond)
					inFlight.Add(-1)
					return nil
				}).Times(6)
			queue := newTestQueue(t, reconciler, tt.parallelism)

			for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
				require.True(t, queue.SubmitReconcile(t.Context(), request(name, "v1", false)))
			}

			waitIdle(t, queue)
			assert.Equal(t, tt.expected, peak.Load())
		})
	}
}

func TestQueueRunsOneOperationPerStackAndCoalescesRequests(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var seen []stackloop.ReconciliationRequest
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req stackloop.ReconciliationRequest) error {
			mu.Lock()
			seen = append(seen, req)
			first := len(seen) == 1
			mu.Unlock()
			if first {
				close(started)
				<-release
			}
			return nil
		}).Times(2)
	queue := newTestQueue(t, reconciler, 4)

	require.True(t, queue.SubmitReconcile(t.Context(), request("app", "v1", false)))
	receive(t, started)
	require.True(t, queue.SubmitReconcile(t.Context(), request("app", "v2", true)))
	require.True(t, queue.SubmitReconcile(t.Context(), request("app", "v3", false)))
	assert.True(t, queue.Busy("app"))
	close(release)

	waitIdle(t, queue)
	assert.False(t, queue.Busy("app"))
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2, "a busy stack never runs twice at once and keeps only the latest waiting request")
	assert.Equal(t, "v1", seen[0].Commit)
	assert.Equal(t, "v3", seen[1].Commit)
	assert.True(t, seen[1].IsManual, "a coalesced manual request stays manual")
}

func TestQueueCleanupRunsOnlyForIdleStacks(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, stackloop.ReconciliationRequest) error {
			close(started)
			<-release
			return nil
		})
	idleStack := config.StackSpec{Name: "idle"}
	idleSource := &compose.File{Path: "idle.yaml"}
	cleaned := make(chan struct{})
	reconciler.EXPECT().Cleanup(gomock.Any(), idleStack, idleSource).DoAndReturn(
		func(context.Context, config.StackSpec, *compose.File) error {
			close(cleaned)
			return nil
		})
	queue := newTestQueue(t, reconciler, 2)

	require.True(t, queue.SubmitReconcile(t.Context(), request("busy", "v1", false)))
	receive(t, started)

	assert.False(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "busy"}, &compose.File{}),
		"cleanup must never overlap a reconciliation of the same stack")
	assert.True(t, queue.SubmitCleanup(t.Context(), idleStack, idleSource))
	receive(t, cleaned)
	close(release)
	waitIdle(t, queue)
}

func TestQueueReconciliationReplacesWaitingCleanup(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).Return(nil)
	// Not started: submissions only queue work until Start.
	queue := New(reconciler, newTestMetrics(t), 1)
	t.Cleanup(queue.Stop)

	require.True(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "app"}, &compose.File{}))
	require.True(t, queue.SubmitReconcile(t.Context(), request("app", "v1", false)))
	queue.Start(context.WithoutCancel(t.Context()))

	waitIdle(t, queue)
}

func TestQueueStopDropsWaitingWorkAndRejectsNewWork(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	reconciler.EXPECT().Reconcile(gomock.Any(), request("running", "v1", false)).DoAndReturn(
		func(context.Context, stackloop.ReconciliationRequest) error {
			close(started)
			<-release
			finished.Store(true)
			return nil
		})
	queue := New(reconciler, newTestMetrics(t), 1)
	queue.Start(context.WithoutCancel(t.Context()))

	require.True(t, queue.SubmitReconcile(t.Context(), request("running", "v1", false)))
	receive(t, started)
	require.True(t, queue.SubmitReconcile(t.Context(), request("waiting", "v1", false)))

	stopped := make(chan struct{})
	go func() {
		queue.Stop()
		close(stopped)
	}()
	require.Eventually(t, func() bool {
		return !queue.SubmitReconcile(t.Context(), request("late", "v1", false))
	}, testWait, time.Millisecond)
	select {
	case <-stopped:
		require.FailNow(t, "Stop must wait for running operations")
	default:
	}

	close(release)
	receive(t, stopped)
	assert.True(t, finished.Load())
	assert.False(t, queue.Busy("waiting"), "waiting work is dropped on Stop")
	waitIdle(t, queue)
}

func TestQueueRetainDropsWaitingWorkOfRemovedStacks(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var seen []string
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req stackloop.ReconciliationRequest) error {
			mu.Lock()
			seen = append(seen, req.Stack.Name+"@"+req.Commit)
			first := len(seen) == 1
			mu.Unlock()
			if first {
				close(started)
				<-release
			}
			return nil
		}).Times(2)
	queue := newTestQueue(t, reconciler, 1)

	require.True(t, queue.SubmitReconcile(t.Context(), request("renamed", "A", false)))
	receive(t, started)
	// The single worker is busy: everything below waits.
	require.True(t, queue.SubmitReconcile(t.Context(), request("renamed", "B", false)))
	require.True(t, queue.SubmitReconcile(t.Context(), request("old", "A", false)))
	require.True(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "removed"}, &compose.File{}))
	require.True(t, queue.SubmitReconcile(t.Context(), request("kept", "A", false)))

	dropped := queue.Retain([]config.StackSpec{{Name: "kept"}, {Name: "new"}})

	assert.Equal(t, []string{"old", "removed", "renamed"}, dropped)
	assert.True(t, queue.Busy("renamed"), "a running operation is not interrupted")
	assert.False(t, queue.Busy("old"))
	assert.False(t, queue.Busy("removed"))
	assert.Nil(t, queue.Retain([]config.StackSpec{{Name: "kept"}}), "nothing left to drop")
	close(release)
	waitIdle(t, queue)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"renamed@A", "kept@A"}, seen,
		"waiting work of removed stacks, including work queued behind a running operation, never runs")
}

func newTestQueue(t *testing.T, reconciler stackloop.StackReconciler, parallelism int) *Queue {
	t.Helper()
	queue := New(reconciler, newTestMetrics(t), parallelism)
	queue.Start(context.WithoutCancel(t.Context()))
	t.Cleanup(queue.Stop)
	return queue
}

func newTestMetrics(t *testing.T) metrics.Sync {
	t.Helper()
	namespace := strings.NewReplacer("/", "_", " ", "_").Replace("test_" + t.Name())
	return metrics.NewGroup(metrics.CreateGroupParams{Namespace: namespace}).Sync
}

func request(stack, commit string, manual bool) stackloop.ReconciliationRequest {
	return stackloop.ReconciliationRequest{
		Stack: config.StackSpec{Name: stack}, Commit: commit, IsManual: manual, Source: &compose.File{Path: stack},
	}
}

func waitIdle(t *testing.T, queue *Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testWait)
	defer cancel()
	require.NoError(t, queue.WaitIdle(ctx), "stack queue did not become idle")
}

func receive(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testWait):
		require.FailNow(t, "timed out waiting for stack operation")
	}
}
