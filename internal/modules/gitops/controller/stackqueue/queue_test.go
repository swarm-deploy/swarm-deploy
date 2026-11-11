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
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
			release := make(chan struct{})
			reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, stackloop.ReconciliationRequest) error {
					current := inFlight.Add(1)
					for {
						observed := peak.Load()
						if current <= observed || peak.CompareAndSwap(observed, current) {
							break
						}
					}
					// Every operation holds its worker until release, so all workers fill up
					// deterministically instead of depending on scheduling within a sleep.
					<-release
					inFlight.Add(-1)
					return nil
				}).Times(6)
			queue := newTestQueue(t, reconciler, tt.parallelism)

			replace(t, queue, request("a", "v1", false), request("b", "v1", false), request("c", "v1", false),
				request("d", "v1", false), request("e", "v1", false), request("f", "v1", false))

			require.Eventually(t, func() bool { return inFlight.Load() == tt.expected }, testWait, time.Millisecond,
				"every worker should pick up an operation")
			close(release)
			waitIdle(t, queue)
			assert.Equal(t, tt.expected, peak.Load(), "no more operations than workers may run at once")
		})
	}
}

func TestQueueKeepsWaitingOrderAcrossRuns(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan string), make(chan struct{})
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req stackloop.ReconciliationRequest) error {
			started <- req.Stack.Name
			<-release
			return nil
		}).Times(4)
	queue := newTestQueue(t, reconciler, 1)
	periodic := []stackloop.ReconciliationRequest{
		request("a", "v1", false), request("b", "v1", false), request("c", "v1", false),
	}

	replace(t, queue, periodic...)
	assert.Equal(t, "a", receiveName(t, started))
	release <- struct{}{}
	assert.Equal(t, "b", receiveName(t, started))
	// The next periodic run arrives while b is running and a has already had its turn:
	// a must wait behind c, otherwise passes longer than the interval starve the last stacks.
	replace(t, queue, periodic...)
	release <- struct{}{}
	assert.Equal(t, "c", receiveName(t, started), "a stack that already ran must not overtake a waiting one")
	release <- struct{}{}
	assert.Equal(t, "a", receiveName(t, started))
	release <- struct{}{}

	waitIdle(t, queue)
}

func TestQueueDropsWaitingWorkWhenRunningOperationIsDesiredAgain(t *testing.T) {
	reverted := request("app", "v1", false)
	reverted.Stack.ComposeFile = "reverted.yaml"
	tests := []struct {
		name     string
		waiting  stackloop.ReconciliationRequest
		expected []string
	}{
		{
			name:     "reverted revision",
			waiting:  request("app", "v2", false),
			expected: []string{"app@v1"},
		},
		{
			name:     "reverted specification",
			waiting:  reverted,
			expected: []string{"app@v1"},
		},
		{
			name:     "waiting manual request still runs manually at the desired revision",
			waiting:  request("app", "v2", true),
			expected: []string{"app@v1", "app@v1(manual)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			reconciler := stackloop.NewMockStackReconciler(ctrl)
			started, release := make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			var seen []string
			reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req stackloop.ReconciliationRequest) error {
					entry := req.Stack.Name + "@" + req.Commit
					if req.IsManual {
						entry += "(manual)"
					}
					mu.Lock()
					seen = append(seen, entry)
					first := len(seen) == 1
					mu.Unlock()
					if first {
						close(started)
						<-release
					}
					return nil
				}).Times(len(tt.expected))
			queue := newTestQueue(t, reconciler, 1)

			replace(t, queue, request("app", "v1", false))
			receive(t, started)
			replace(t, queue, tt.waiting)
			replace(t, queue, request("app", "v1", false))
			close(release)

			waitIdle(t, queue)
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tt.expected, seen, "work superseded by the running operation must never run")
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

func TestQueueSkipsRepeatedIntervalDuringRunningReconciliation(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	reconciler.EXPECT().Reconcile(gomock.Any(), request("app", "v1", false)).DoAndReturn(
		func(context.Context, stackloop.ReconciliationRequest) error {
			close(started)
			<-release
			return nil
		})
	queue := newTestQueue(t, reconciler, 1)
	replace(t, queue, request("app", "v1", false))
	receive(t, started)
	for range 3 {
		replace(t, queue, request("app", "v1", false))
	}
	assert.False(t, queueBusyPending(queue, "app"))
	close(release)
	waitIdle(t, queue)
}

func TestQueueManualRequestDuringRunningReconciliation(t *testing.T) {
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
	queue := newTestQueue(t, reconciler, 1)
	replace(t, queue, request("app", "v1", false))
	receive(t, started)
	replace(t, queue, request("app", "v1", true))
	replace(t, queue, request("app", "v1", false))
	close(release)
	waitIdle(t, queue)
	require.Len(t, seen, 2)
	assert.True(t, seen[1].IsManual)
}

func TestQueueClearPreservesChangedPriority(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	var mu sync.Mutex
	var order []string
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req stackloop.ReconciliationRequest) error {
			mu.Lock()
			order = append(order, req.Stack.Name)
			mu.Unlock()
			return nil
		}).Times(3)
	queue := New(reconciler, newTestMetrics(t), 1)
	t.Cleanup(queue.Stop)
	replaceChanged(t, queue, []string{"changed"}, request("changed", "A", false), request("other", "A", false))
	assert.Equal(t, []string{"changed", "other"}, queue.Clear(map[string]bool{"failed-run-change": true}))
	replace(t, queue, request("other", "B", false), request("failed-run-change", "B", false), request("changed", "B", false))
	queue.Start(context.WithoutCancel(t.Context()))
	waitIdle(t, queue)
	assert.Equal(t, []string{"failed-run-change", "changed", "other"}, order)
}

func TestQueueOperationUsesSpanLink(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).Return(nil)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	defer func() { _ = provider.Shutdown(t.Context()) }()
	queue := New(reconciler, newTestMetrics(t), 1)
	queue.tracer = provider.Tracer("test.queue")
	t.Cleanup(queue.Stop)
	ctx, dispatch := provider.Tracer("test.dispatch").Start(t.Context(), "dispatch")
	_, accepted := queue.Replace(ctx, []stackloop.ReconciliationRequest{request("app", "v1", false)}, nil)
	require.True(t, accepted)
	dispatchContext := dispatch.SpanContext()
	dispatch.End()
	queue.Start(context.WithoutCancel(t.Context()))
	waitIdle(t, queue)

	var operationSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "controller.stackOperation" {
			operationSpan = span
			break
		}
	}
	require.NotNil(t, operationSpan)
	assert.False(t, operationSpan.Parent().IsValid())
	require.Len(t, operationSpan.Links(), 1)
	assert.Equal(t, dispatchContext, operationSpan.Links()[0].SpanContext)
}

func queueBusyPending(queue *Queue, name string) bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	_, pending := queue.pending[name]
	return pending
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

func receiveName(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case name := <-ch:
		return name
	case <-time.After(testWait):
		require.FailNow(t, "timed out waiting for stack operation")
		return ""
	}
}

func receive(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testWait):
		require.FailNow(t, "timed out waiting for stack operation")
	}
}

func TestPausedAdmissionPreservesOrReplacesPendingWork(t *testing.T) {
	for _, replacePending := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload failure resumes old work", true: "accepted configuration replaces old work"}[replacePending], func(t *testing.T) {
			reconciler := stackloop.NewMockStackReconciler(gomock.NewController(t))
			queue := New(reconciler, newTestMetrics(t), 2)
			t.Cleanup(queue.Stop)
			require.True(t, queue.Pause())
			replace(t, queue, request("app", "A", false))
			queue.Start(context.WithoutCancel(t.Context()))
			assert.False(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "other"}, "A"))
			expectedCommit := "A"
			if replacePending {
				replace(t, queue, request("app", "B", false))
				expectedCommit = "B"
			}
			reconciler.EXPECT().Reconcile(gomock.Any(), request("app", expectedCommit, false)).Return(nil)
			queue.Resume()
			waitIdle(t, queue)
		})
	}
}

func TestStopDoesNotWaitForPausedAdmission(t *testing.T) {
	reconciler := stackloop.NewMockStackReconciler(gomock.NewController(t))
	queue := New(reconciler, newTestMetrics(t), 2)
	require.True(t, queue.Pause())
	replace(t, queue, request("app", "A", false))
	queue.Start(context.WithoutCancel(t.Context()))
	stopped := make(chan struct{})
	go func() {
		queue.Stop()
		close(stopped)
	}()
	receive(t, stopped)
	queue.Resume()
	assert.False(t, queue.Pause())
	waitIdle(t, queue)
}
