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
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"go.uber.org/mock/gomock"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

			replace(t, queue, request("a", "v1", false), request("b", "v1", false), request("c", "v1", false),
				request("d", "v1", false), request("e", "v1", false), request("f", "v1", false))

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

	replace(t, queue, request("app", "v1", false))
	receive(t, started)
	replace(t, queue, request("app", "v2", true))
	replace(t, queue, request("app", "v3", false))
	assert.True(t, queueBusy(queue, "app"))
	close(release)

	waitIdle(t, queue)
	assert.False(t, queueBusy(queue, "app"))
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
	cleaned := make(chan struct{})
	reconciler.EXPECT().Cleanup(gomock.Any(), idleStack, "v1").DoAndReturn(
		func(context.Context, config.StackSpec, string) error {
			close(cleaned)
			return nil
		})
	queue := newTestQueue(t, reconciler, 2)

	replace(t, queue, request("busy", "v1", false))
	receive(t, started)

	assert.False(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "busy"}, "v1"),
		"cleanup must never overlap a reconciliation of the same stack")
	assert.True(t, queue.SubmitCleanup(t.Context(), idleStack, "v1"))
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

	require.True(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "app"}, "v1"))
	replace(t, queue, request("app", "v1", false))
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

	replace(t, queue, request("running", "v1", false))
	receive(t, started)
	replace(t, queue, request("waiting", "v1", false))

	stopped := make(chan struct{})
	go func() {
		queue.Stop()
		close(stopped)
	}()
	require.Eventually(t, func() bool {
		_, accepted := queue.Replace(t.Context(), []stackloop.ReconciliationRequest{
			request("waiting", "v1", false), request("late", "v1", false),
		}, nil)
		return !accepted
	}, testWait, time.Millisecond)
	select {
	case <-stopped:
		require.FailNow(t, "Stop must wait for running operations")
	default:
	}

	close(release)
	receive(t, stopped)
	assert.True(t, finished.Load())
	assert.False(t, queueBusy(queue, "waiting"), "waiting work is dropped on Stop")
	assert.False(t, queueBusy(queue, "late"), "waiting work is dropped on Stop")
	waitIdle(t, queue)
}

func TestQueueReplaceDropsWaitingWorkOfStacksAbsentFromDesiredState(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	seen := recordReconciliations(reconciler, started, release, 3)
	queue := newTestQueue(t, reconciler, 1)

	replace(t, queue, request("renamed", "A", false))
	receive(t, started)
	// The single worker is busy: everything below waits.
	replace(t, queue, request("renamed", "B", false), request("old", "B", false), request("kept", "B", false))
	require.True(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "removed"}, "B"))

	dropped := replace(t, queue, request("kept", "C", false), request("new", "C", false))

	assert.Equal(t, []string{"old", "removed", "renamed"}, dropped)
	assert.True(t, queueBusy(queue, "renamed"), "a running operation is not interrupted")
	assert.False(t, queueBusy(queue, "old"))
	assert.False(t, queueBusy(queue, "removed"))
	close(release)
	waitIdle(t, queue)
	assert.Equal(t, []string{"renamed@A", "kept@C", "new@C"}, seen(),
		"waiting work absent from the latest desired state never runs, even behind a running operation")
}

func TestQueueRunsChangedStacksFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	seen := recordReconciliations(reconciler, started, release, 4)
	queue := newTestQueue(t, reconciler, 1)

	replace(t, queue, request("a", "1", false))
	receive(t, started)
	// Revision 2 changes nothing: b and c wait in configuration order.
	replace(t, queue, request("a", "2", false), request("b", "2", false), request("c", "2", false))
	// Revision 3 changes c and the running a: the planner puts them first.
	replaceChanged(t, queue, []string{"c", "a"},
		request("c", "3", false), request("a", "3", false), request("b", "3", false))
	// A periodic run without a diff must not push the changed stacks back.
	replace(t, queue, request("a", "4", false), request("b", "4", false), request("c", "4", false))
	close(release)

	waitIdle(t, queue)
	assert.Equal(t, []string{"a@1", "c@4", "a@4", "b@4"}, seen(),
		"waiting changed stacks run before unchanged ones, including a changed stack that was running")
}

func TestQueueClearDropsAllWaitingWork(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	seen := recordReconciliations(reconciler, started, release, 1)
	queue := newTestQueue(t, reconciler, 1)

	replace(t, queue, request("running", "A", false))
	receive(t, started)
	replace(t, queue, request("running", "B", false), request("waiting", "B", false))

	assert.Equal(t, []string{"running", "waiting"}, queue.Clear(nil))
	assert.Nil(t, queue.Clear(nil), "nothing left to drop")
	assert.True(t, queueBusy(queue, "running"), "a running operation is not interrupted")
	assert.False(t, queueBusy(queue, "waiting"))
	close(release)
	waitIdle(t, queue)
	assert.Equal(t, []string{"running@A"}, seen())
}

// queueBusy inspects the private queue state solely for queue unit tests.
func queueBusy(queue *Queue, name string) bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	_, pending := queue.pending[name]
	_, running := queue.running[name]
	return pending || running
}

// recordReconciliations blocks the first reconciliation until release and returns the observed order.
func recordReconciliations(
	reconciler *stackloop.MockStackReconciler, started, release chan struct{}, times int,
) func() []string {
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
		}).Times(times)
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func replace(t *testing.T, queue *Queue, requests ...stackloop.ReconciliationRequest) []string {
	t.Helper()
	return replaceChanged(t, queue, nil, requests...)
}

func replaceChanged(
	t *testing.T, queue *Queue, changed []string, requests ...stackloop.ReconciliationRequest,
) []string {
	t.Helper()
	changedSet := make(map[string]bool, len(changed))
	for _, name := range changed {
		changedSet[name] = true
	}
	dropped, accepted := queue.Replace(t.Context(), requests, changedSet)
	require.True(t, accepted)
	return dropped
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
		Stack: config.StackSpec{Name: stack}, Commit: commit, IsManual: manual,
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
