// Package stackqueue runs stack operations with bounded parallelism and at most one operation per stack.
//
// The queue is keyed by stack name, like a Kubernetes work queue, and holds for every stack only the
// revision to apply: operations read their sources at that revision from Git. Every successful sync run
// replaces the whole set of waiting reconciliations (Replace), so waiting work always reflects the latest
// successfully accepted configuration and revision: stacks that are no longer configured simply have
// nothing left to run, and a stack never runs with a stale specification or revision once a newer run
// has replaced it. A run that fails after accepting a new configuration clears all waiting work (Clear)
// instead of letting older revisions run under it.
//
// Stacks changed in Git run first, and a waiting stack keeps its changed mark until it runs, so a later
// run without a diff (for example, the periodic one) does not push it back. Within each group the queue
// is first-in, first-out: a stack that is already waiting keeps its place, and a stack that ran and is
// queued again waits behind the stacks that have not had their turn yet. A run that takes longer than
// the sync interval therefore cannot starve the stacks at the end of the configuration.
//
// A slow stack never delays Git polling, later sync runs or other stacks while a worker is free.
// Running operations are never interrupted by Replace or Clear; they finish within their lifecycle.
package stackqueue

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const (
	// OperationReconcile applies a stack at a revision.
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
	// changed marks work for a stack whose source changed in Git; such work is scheduled first.
	changed bool
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
	running map[string]operation
	// changedAfterFailure retains priority when a failed run drops pending work.
	changedAfterFailure map[string]bool
	paused              bool
	started             bool
	stopped             bool
	idle                chan struct{}
	stateChanged        chan struct{}
	done                sync.WaitGroup
}

// New creates a queue with the given number of workers. Values below one mean sequential processing.
func New(reconciler stackloop.StackReconciler, syncMetrics metrics.Sync, parallelism int) *Queue {
	idle := make(chan struct{})
	close(idle)
	q := &Queue{
		reconciler: reconciler,
		metrics:    syncMetrics,
		workers:    max(parallelism, 1),
		tracer: otel.Tracer(
			"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackqueue",
		),
		pending:             map[string]operation{},
		running:             map[string]operation{},
		changedAfterFailure: map[string]bool{},
		idle:                idle,
		stateChanged:        make(chan struct{}),
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
	clear(q.changedAfterFailure)
	q.updateStateLocked()
	q.cond.Broadcast()
	q.mu.Unlock()

	q.done.Wait()
}

// Pause prevents pending work from becoming running while configuration is accepted.
// Running operations continue. No queue lock is held after this call returns.
// The serialized sync runner must pair a successful Pause with Resume.
func (q *Queue) Pause() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return false
	}
	q.paused = true
	return true
}

// Resume admits workers after pending work has been replaced or cleared.
func (q *Queue) Resume() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.paused = false
	q.cond.Broadcast()
}

// Replace atomically makes requests the complete set of reconciliations waiting in the queue and returns
// the sorted names of stacks whose waiting work was dropped because they are absent from requests.
// changed names stacks whose source changed in Git; it may be nil.
//
// A reconciliation already waiting for a stack is superseded, keeping the manual and changed marks of
// either request, and a waiting cleanup is superseded too. A non-manual request identical to the running
// operation of a stack drops the work waiting for that stack: the desired state is already being applied.
// Changed stacks wait before the others; within each group stacks that were already waiting keep their
// order and newly queued stacks follow in request order. It returns false after Stop.
func (q *Queue) Replace(
	ctx context.Context, requests []stackloop.ReconciliationRequest, changed map[string]bool,
) ([]string, bool) {
	spanContext := trace.SpanContextFromContext(ctx)
	desired := make(map[string]struct{}, len(requests))
	for _, req := range requests {
		desired[req.Stack.Name] = struct{}{}
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return nil, false
	}

	dropped := q.dropLocked(func(name string) bool {
		_, isDesired := desired[name]
		return !isDesired
	})
	for _, req := range requests {
		name := req.Stack.Name
		op := operation{
			kind: OperationReconcile, request: req, spanContext: spanContext,
			changed: changed[name] || q.changedAfterFailure[name],
		}
		if previous, hasPrevious := q.pending[name]; hasPrevious {
			op.changed = op.changed || previous.changed
			if previous.kind == OperationReconcile {
				op.request.IsManual = op.request.IsManual || previous.request.IsManual
			}
		}
		// The running operation already applies the desired revision and specification, so any work
		// waiting for the stack is stale: for example, a revision that was reverted while it waited.
		// Manual requests (including a manual mark inherited from waiting work) still run again.
		if running, busy := q.running[name]; busy && running.kind == OperationReconcile &&
			running.request.Commit == req.Commit && reflect.DeepEqual(running.request.Stack, req.Stack) &&
			!op.request.IsManual {
			delete(q.pending, name)
			continue
		}
		q.pending[name] = op
	}
	clear(q.changedAfterFailure)
	q.rebuildReadyLocked(requests)
	q.updateStateLocked()
	return dropped, true
}

// rebuildReadyLocked orders every waiting stack that is not running: changed stacks first, then the
// others. Within each group stacks already in the ready list keep their order, so re-queuing a stack
// never moves it ahead of stacks that have been waiting longer; stacks that were not waiting follow in
// request order. Running stacks re-enter the ready list through finish.
func (q *Queue) rebuildReadyLocked(requests []stackloop.ReconciliationRequest) {
	previous := q.ready
	ready := make([]string, 0, len(requests))
	seen := make(map[string]struct{}, len(requests))
	add := func(name string, changedPass bool) {
		if _, isSeen := seen[name]; isSeen || q.isRunningLocked(name) {
			return
		}
		op, isPending := q.pending[name]
		if !isPending || op.changed != changedPass {
			return
		}
		seen[name] = struct{}{}
		ready = append(ready, name)
	}
	for _, changedPass := range []bool{true, false} {
		for _, name := range previous {
			add(name, changedPass)
		}
		for _, req := range requests {
			add(req.Stack.Name, changedPass)
		}
	}
	q.ready = ready
	q.cond.Broadcast()
}

// Clear drops every waiting operation but preserves changed-stack priority for the next
// successful run. changed also contains changes detected by the failed run itself.
// Running operations are not interrupted.
func (q *Queue) Clear(changed map[string]bool) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	for name, pending := range q.pending {
		if pending.kind == OperationReconcile && pending.changed {
			q.changedAfterFailure[name] = true
		}
	}
	for name, isChanged := range changed {
		if isChanged {
			q.changedAfterFailure[name] = true
		}
	}
	dropped := q.dropLocked(func(string) bool { return true })
	q.updateStateLocked()
	return dropped
}

func (q *Queue) isRunningLocked(name string) bool {
	_, running := q.running[name]
	return running
}

// SubmitCleanup queues a rotated-resource cleanup only when the stack has no running or waiting work.
// A busy stack is skipped: cleanup is periodic and runs on a later tick. It returns whether it was queued.
func (q *Queue) SubmitCleanup(ctx context.Context, stack config.StackSpec, commit string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped || q.paused || q.isRunningLocked(stack.Name) {
		return false
	}
	if _, hasPending := q.pending[stack.Name]; hasPending {
		return false
	}

	q.pending[stack.Name] = operation{
		kind:        OperationCleanup,
		request:     stackloop.ReconciliationRequest{Stack: stack, Commit: commit},
		spanContext: trace.SpanContextFromContext(ctx),
	}
	q.enqueueLocked(stack.Name)
	q.updateStateLocked()
	return true
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

// WaitStackIdle blocks until the named stack has no waiting or running operation.
func (q *Queue) WaitStackIdle(ctx context.Context, stack string) error {
	for {
		q.mu.Lock()
		_, pending := q.pending[stack]
		_, running := q.running[stack]
		changed := q.stateChanged
		q.mu.Unlock()
		if !pending && !running {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
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
	for (len(q.ready) == 0 || q.paused) && !q.stopped {
		q.cond.Wait()
	}
	if q.stopped {
		return "", operation{}, false
	}

	name := q.ready[0]
	q.ready = q.ready[1:]
	op := q.pending[name]
	delete(q.pending, name)
	q.running[name] = op
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
	options := []trace.SpanStartOption{trace.WithNewRoot(), trace.WithAttributes(
		tracing.ResourceStackName.String(op.request.Stack.Name),
		tracing.SyncCommitSha.String(op.request.Commit),
	)}
	if op.spanContext.IsValid() {
		options = append(options, trace.WithLinks(trace.Link{SpanContext: op.spanContext}))
	}
	ctx, span := q.tracer.Start(ctx, "controller.stackOperation", options...)
	defer span.End()
	startedAt := time.Now()

	var err error
	switch op.kind {
	case OperationCleanup:
		err = q.reconciler.Cleanup(ctx, op.request.Stack, op.request.Commit)
		if err != nil {
			slog.WarnContext(ctx, "[controller] rotated resource cleanup failed",
				slog.String("stack", op.request.Stack.Name), slog.Any("err", err))
		}
	default:
		err = q.reconciler.Reconcile(ctx, op.request)
		if err != nil {
			slog.ErrorContext(ctx, "[controller] sync failed for stack",
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

// dropLocked removes waiting operations of matching stacks and returns their sorted names.
func (q *Queue) dropLocked(matches func(name string) bool) []string {
	var dropped []string
	for name := range q.pending {
		if matches(name) {
			delete(q.pending, name)
			dropped = append(dropped, name)
		}
	}
	if len(dropped) == 0 {
		return nil
	}
	// The ready list holds only stacks with waiting work, so dropped stacks leave it as well.
	q.ready = slices.DeleteFunc(q.ready, matches)
	slices.Sort(dropped)
	return dropped
}

// enqueueLocked adds a stack with waiting work to the ready list: changed work after the last changed
// stack, any other work at the end.
func (q *Queue) enqueueLocked(name string) {
	if q.pending[name].changed {
		position := 0
		for position < len(q.ready) && q.pending[q.ready[position]].changed {
			position++
		}
		q.ready = slices.Insert(q.ready, position, name)
	} else {
		q.ready = append(q.ready, name)
	}
	q.cond.Signal()
}

// updateStateLocked publishes queue gauges and maintains the idle notification channel.
func (q *Queue) updateStateLocked() {
	q.metrics.SetStackQueue(len(q.pending), len(q.running))
	close(q.stateChanged)
	q.stateChanged = make(chan struct{})

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
