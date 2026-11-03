// Package stackqueue runs stack operations with bounded parallelism and at most one operation per stack.
//
// A sync run only loads stack sources and submits them here, so a slow stack never delays Git polling,
// later sync runs or other stacks while a worker is free. Work for one stack is coalesced: while a stack
// is busy, only the latest submitted reconciliation is kept and runs right after the current one.
package stackqueue

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const (
	// OperationReconcile applies a stack source.
	OperationReconcile = "reconcile"
	// OperationCleanup removes stale rotated configs and secrets of a stack.
	OperationCleanup = "cleanup"

	resultSuccess = "success"
	resultError   = "error"
)

type operation struct {
	kind        string
	request     stackloop.ReconciliationRequest
	spanContext trace.SpanContext
}

// Queue executes stack operations on a fixed number of workers.
type Queue struct {
	reconciler stackloop.StackReconciler
	metrics    metrics.Sync
	workers    int
	tracer     trace.Tracer

	mu      sync.Mutex
	cond    *sync.Cond
	ready   []string
	pending map[string]operation
	running map[string]bool
	started bool
	stopped bool
	idle    chan struct{}
	done    sync.WaitGroup
}

// New creates a queue with the given number of workers. Values below one mean sequential processing.
func New(reconciler stackloop.StackReconciler, syncMetrics metrics.Sync, parallelism int) *Queue {
	idle := make(chan struct{})
	close(idle)
	q := &Queue{
		reconciler: reconciler,
		metrics:    syncMetrics,
		workers:    max(parallelism, 1),
		tracer:     otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackqueue"),
		pending:    map[string]operation{},
		running:    map[string]bool{},
		idle:       idle,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Start launches the workers. Operations run with ctx, which must outlive Stop.
func (q *Queue) Start(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.started || q.stopped {
		return
	}
	q.started = true
	for range q.workers {
		q.done.Go(func() { q.work(ctx) })
	}
}

// Stop rejects new work, drops operations that have not started and waits for running ones.
// Dropped work is not lost: the next sync run after a restart reconciles every stack again.
func (q *Queue) Stop() {
	q.mu.Lock()
	q.stopped = true
	q.ready = nil
	clear(q.pending)
	q.updateStateLocked()
	q.cond.Broadcast()
	q.mu.Unlock()

	q.done.Wait()
}

// SubmitReconcile queues a reconciliation. A reconciliation already waiting for the same stack is
// replaced, keeping the manual flag of either request. It returns false after Stop.
func (q *Queue) SubmitReconcile(ctx context.Context, req stackloop.ReconciliationRequest) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return false
	}

	name := req.Stack.Name
	previous, hasPrevious := q.pending[name]
	if hasPrevious && previous.kind == OperationReconcile {
		req.IsManual = req.IsManual || previous.request.IsManual
	}
	q.pending[name] = operation{
		kind: OperationReconcile, request: req, spanContext: trace.SpanContextFromContext(ctx),
	}
	// A stack is in the ready list only while it has waiting work and nothing running;
	// finish re-enqueues work submitted while the stack was running.
	if !hasPrevious && !q.running[name] {
		q.enqueueLocked(name)
	}
	q.updateStateLocked()
	return true
}

// SubmitCleanup queues a rotated-resource cleanup only when the stack has no running or waiting work.
// A busy stack is skipped: cleanup is periodic and runs on a later tick. It returns whether it was queued.
func (q *Queue) SubmitCleanup(ctx context.Context, stack config.StackSpec, source *compose.File) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped || q.running[stack.Name] {
		return false
	}
	if _, hasPending := q.pending[stack.Name]; hasPending {
		return false
	}

	q.pending[stack.Name] = operation{
		kind:        OperationCleanup,
		request:     stackloop.ReconciliationRequest{Stack: stack, Source: source},
		spanContext: trace.SpanContextFromContext(ctx),
	}
	q.enqueueLocked(stack.Name)
	q.updateStateLocked()
	return true
}

// Retain drops waiting operations of stacks that are absent from the current configuration and
// returns their names in sorted order. Running operations are not interrupted: they finish within
// their normal lifecycle. Without this, work queued for a removed or renamed stack would still be
// applied from an older revision after the new configuration took effect.
func (q *Queue) Retain(stacks []config.StackSpec) []string {
	configured := make(map[string]struct{}, len(stacks))
	for _, stack := range stacks {
		configured[stack.Name] = struct{}{}
	}
	isRemoved := func(name string) bool {
		_, ok := configured[name]
		return !ok
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	var dropped []string
	for name := range q.pending {
		if isRemoved(name) {
			delete(q.pending, name)
			dropped = append(dropped, name)
		}
	}
	if len(dropped) == 0 {
		return nil
	}
	// The ready list holds only stacks with waiting work, so removed stacks leave it as well.
	q.ready = slices.DeleteFunc(q.ready, isRemoved)
	q.updateStateLocked()
	slices.Sort(dropped)
	return dropped
}

// Busy reports whether a stack has an operation running or waiting to start.
func (q *Queue) Busy(stack string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, hasPending := q.pending[stack]
	return hasPending || q.running[stack]
}

// WaitIdle blocks until no operation is waiting or running, or ctx is done.
func (q *Queue) WaitIdle(ctx context.Context) error {
	q.mu.Lock()
	idle := q.idle
	q.mu.Unlock()

	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *Queue) work(ctx context.Context) {
	for {
		name, op, ok := q.next()
		if !ok {
			return
		}
		q.execute(ctx, op)
		q.finish(name)
	}
}

func (q *Queue) next() (string, operation, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.ready) == 0 && !q.stopped {
		q.cond.Wait()
	}
	if q.stopped {
		return "", operation{}, false
	}

	name := q.ready[0]
	q.ready = q.ready[1:]
	op := q.pending[name]
	delete(q.pending, name)
	q.running[name] = true
	q.updateStateLocked()
	return name, op, true
}

func (q *Queue) finish(name string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.running, name)
	if _, hasPending := q.pending[name]; hasPending {
		q.enqueueLocked(name)
	}
	q.updateStateLocked()
}

func (q *Queue) execute(ctx context.Context, op operation) {
	if op.spanContext.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, op.spanContext)
	}
	ctx, span := q.tracer.Start(ctx, "controller.stackOperation", trace.WithAttributes(
		tracing.ResourceStackName.String(op.request.Stack.Name),
		tracing.SyncCommitSha.String(op.request.Commit),
	))
	defer span.End()
	startedAt := time.Now()

	var err error
	switch op.kind {
	case OperationCleanup:
		err = q.reconciler.Cleanup(ctx, op.request.Stack, op.request.Source)
		if err != nil {
			slog.WarnContext(ctx, "[controller] rotated resource cleanup failed",
				slog.String("stack", op.request.Stack.Name), slog.Any("error", err))
		}
	default:
		err = q.reconciler.Reconcile(ctx, op.request)
		if err != nil {
			slog.ErrorContext(ctx, "sync failed for stack",
				slog.String("stack", op.request.Stack.Name), slog.String("commit", op.request.Commit),
				slog.Bool("manual", op.request.IsManual), slog.Any("err", err))
		}
	}

	result := resultSuccess
	if err != nil {
		result = resultError
		tracing.FailSpan(span, err)
	}
	q.metrics.RecordStackOperation(op.request.Stack.Name, op.kind, result, time.Since(startedAt))
}

func (q *Queue) enqueueLocked(name string) {
	q.ready = append(q.ready, name)
	q.cond.Signal()
}

// updateStateLocked publishes queue gauges and maintains the idle notification channel.
func (q *Queue) updateStateLocked() {
	q.metrics.SetStackQueue(len(q.pending), len(q.running))

	busy := len(q.pending) > 0 || len(q.running) > 0
	select {
	case <-q.idle:
		if busy {
			q.idle = make(chan struct{})
		}
	default:
		if !busy {
			close(q.idle)
		}
	}
}
