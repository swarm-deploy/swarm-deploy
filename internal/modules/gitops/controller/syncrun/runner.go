// Package syncrun executes serialized GitOps synchronization runs.
//
// A run pulls Git, reloads configuration, reconciles networks and replaces the work waiting in the
// stack queue with every configured stack at the run's revision. It does not wait for stack
// deployments, so a slow stack never delays polling, later runs or other stacks. Stacks read their
// sources at that revision from Git, so later pulls never change what a queued operation applies.
package syncrun

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/gitpull"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackplan"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackqueue"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TriggerReason identifies why a synchronization was requested.
type TriggerReason string

const (
	// TriggerStartup is the initial reconciliation after ownership is acquired.
	TriggerStartup TriggerReason = "startup"
	// TriggerPoll follows a Git poll that found a changed revision.
	TriggerPoll TriggerReason = "poll"
	// TriggerInterval is a periodic reconciliation without a Git pull.
	TriggerInterval TriggerReason = "interval"
	// TriggerWebhook is requested by the webhook entry point.
	TriggerWebhook TriggerReason = "webhook"
	// TriggerManual is requested by an authenticated user.
	TriggerManual TriggerReason = "manual"
)

const (
	syncRunResultError    = "error"
	syncRunResultNoChange = "no_change"
	syncRunResultUpdated  = "updated"
	syncRunResultSuccess  = "success"
)

// errQueueStopped reports a run whose stacks were not queued because the controller is shutting down.
var errQueueStopped = errors.New("stack queue is stopped: controller is shutting down")

// dispatch is the latest set of stacks accepted by the stack queue and the revision they were queued at.
type dispatch struct {
	commit string
	stacks []config.StackSpec
}

// Task describes one queued synchronization request.
type Task struct {
	TriggeredBy string
	Reason      TriggerReason
	SpanContext trace.SpanContext
}

// Runner executes Git polling, complete synchronization runs and scheduled cleanup.
type Runner struct {
	configStore       *configstore.ConfigStore
	git               gitx.Repository
	metrics           *metrics.Group
	event             dispatcher.Dispatcher
	stateStore        modelstore.Store
	networkReconciler *networkloop.Coordinator
	stackPlanner      *stackplan.Planner
	puller            *gitpull.Puller
	stacks            *stackqueue.Queue
	tracer            trace.Tracer

	dispatchMu   sync.Mutex
	lastDispatch *dispatch
}

// New creates a synchronization runner from the existing GitOps components.
func New(
	configStore *configstore.ConfigStore,
	git gitx.Repository,
	metricGroup *metrics.Group,
	eventDispatcher dispatcher.Dispatcher,
	stateStore modelstore.Store,
	networkReconciler *networkloop.Coordinator,
	puller *gitpull.Puller,
	stacks *stackqueue.Queue,
) *Runner {
	return &Runner{
		configStore: configStore, git: git, metrics: metricGroup, event: eventDispatcher,
		stateStore: stateStore, networkReconciler: networkReconciler,
		stackPlanner: stackplan.NewPlanner(git), puller: puller, stacks: stacks,
		tracer: otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"),
	}
}

// Poll checks Git and runs reconciliation only when the revision changed.
func (r *Runner) Poll(ctx context.Context) {
	ctx, span := r.tracer.Start(ctx, "controller.Poll", trace.WithAttributes(
		tracing.SyncReason.String(string(TriggerPoll)),
	))
	defer span.End()

	gitResult, err := r.puller.Pull(ctx, string(TriggerPoll))
	if err != nil {
		tracing.FailSpan(span, err)
		r.updatePollState(ctx, syncRunResultError, err)
		return
	}

	pollResult := syncRunResultNoChange
	if gitResult.Updated {
		pollResult = syncRunResultUpdated
	}
	r.updatePollState(ctx, pollResult, nil)
	span.AddEvent("git pull completed", trace.WithAttributes(tracing.SyncGitUpdated.Bool(gitResult.Updated)))
	if gitResult.Updated {
		r.run(ctx, Task{Reason: TriggerPoll}, &gitResult)
	}
}

// Run executes one GitOps synchronization run and dispatches stacks to the stack queue.
func (r *Runner) Run(ctx context.Context, task Task) {
	r.run(ctx, task, nil)
}

func (r *Runner) run(ctx context.Context, task Task, gitResult *gitx.PullResult) {
	if task.SpanContext.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, task.SpanContext)
	}
	ctx, span := r.tracer.Start(ctx, "controller.Sync", trace.WithAttributes(
		tracing.SyncTrigger.String(string(task.Reason)),
	))
	defer span.End()
	startedAt := time.Now()

	if err := r.publishManualSync(ctx, task); err != nil {
		tracing.FailSpan(span, err)
		slog.ErrorContext(ctx, "persist manual sync event", "err", err)
		r.metrics.Sync.RecordSyncRun(string(task.Reason), syncRunResultError, time.Since(startedAt))
		return
	}

	resolvedGitResult, err := r.resolveGitState(ctx, task.Reason, gitResult)
	if err != nil {
		tracing.FailSpan(span, err)
		r.metrics.Sync.RecordSyncRun(string(task.Reason), syncRunResultError, time.Since(startedAt))
		_ = r.updateState(ctx, func(state *model.Runtime) {
			state.LastSyncAt, state.LastSyncReason = time.Now(), string(task.Reason)
			state.LastSyncResult, state.LastSyncError = syncRunResultError, err.Error()
		})
		return
	}
	gitResult = &resolvedGitResult

	slog.InfoContext(ctx, "[controller] run sync", slog.String("reason", string(task.Reason)))
	// Reload accepts configuration before returning (including event publication).
	// Close admission first, without holding the queue mutex across reload or I/O.
	if !r.stacks.Pause() {
		r.finishStageFailure(ctx, task.Reason, startedAt, gitResult.NewRevision, "stacks dispatch", errQueueStopped)
		return
	}
	defer r.stacks.Resume()
	if err = r.configStore.Reload(ctx, r.git.WorkingDir()); err != nil {
		r.finishStageFailure(ctx, task.Reason, startedAt, gitResult.NewRevision, "configuration reload", err)
		return
	}
	// Cleanup must not recreate operations from the previously accepted configuration.
	r.invalidateDispatch()
	slog.InfoContext(ctx, "[controller] configuration reloaded",
		slog.Int("stacks", len(r.configStore.Stacks())), slog.Int("networks", len(r.configStore.Networks())))

	// Plan first so that changes detected by a run that fails during network reconciliation
	// still receive priority on the next successful (possibly periodic) run.
	plan := r.stackPlanner.Plan(ctx, r.configStore.Stacks(), *gitResult)
	if plan.DiffErr != nil {
		slog.ErrorContext(ctx, "git diff failed, continue with default stack order",
			slog.String("reason", string(task.Reason)), slog.String("old_revision", gitResult.OldRevision),
			slog.String("new_revision", gitResult.NewRevision), slog.Any("err", plan.DiffErr))
	}

	if err = r.networkReconciler.Reconcile(ctx, gitResult.NewRevision); err != nil {
		// The configuration of this revision is already accepted, so waiting work prepared for an older
		// configuration must not run under it. The next successful run queues every stack again.
		if dropped := r.stacks.Clear(plan.Changed); len(dropped) > 0 {
			slog.WarnContext(ctx, "[controller] dropped queued stack operations after a failed sync run",
				slog.Any("stacks", dropped), slog.String("commit", gitResult.NewRevision))
		}
		r.finishStageFailure(ctx, task.Reason, startedAt, gitResult.NewRevision, "networks reconcile", err)
		return
	}

	stackCtx, stackSpan := r.tracer.Start(ctx, "controller.dispatchStacks", trace.WithAttributes(
		tracing.StacksTotal.Int(len(plan.Stacks)), tracing.StacksChanged.Int(len(plan.Changed))))
	if err = r.dispatchStacks(stackCtx, plan, gitResult.NewRevision, task.Reason); err != nil {
		tracing.FailSpan(stackSpan, err)
		stackSpan.End()
		tracing.FailSpan(span, err)
		r.finishStageFailure(ctx, task.Reason, startedAt, gitResult.NewRevision, "stacks dispatch", err)
		return
	}
	stackSpan.SetStatus(codes.Ok, "")
	stackSpan.End()

	// Stack outcomes are tracked per stack; the run result covers Git, configuration, networks and dispatch.
	result := syncRunResultSuccess
	persistErr := r.updateState(ctx, func(state *model.Runtime) {
		state.LastSyncAt, state.LastSyncReason, state.LastSyncResult = time.Now(), string(task.Reason), result
		state.LastSyncError = ""
		state.GitRevision = gitResult.NewRevision
	})
	if persistErr != nil {
		result = syncRunResultError
		tracing.FailSpan(span, persistErr)
	}
	r.metrics.Sync.RecordSyncRun(string(task.Reason), result, time.Since(startedAt))
}

func (r *Runner) invalidateDispatch() {
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	r.lastDispatch = nil
}

// Cleanup queues rotated-resource cleanup for idle stacks of the latest accepted dispatch.
// Accepted configurations invalidate the previous dispatch, including when network reconciliation fails.
// Busy stacks are skipped until a later tick, so cleanup never overlaps a reconciliation of the same stack.
func (r *Runner) Cleanup(ctx context.Context) {
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	latest := r.lastDispatch
	if latest == nil {
		slog.DebugContext(ctx, "[controller] rotated resource cleanup skipped: no stacks dispatched yet")
		return
	}
	for _, stack := range latest.stacks {
		if !r.stacks.SubmitCleanup(ctx, stack, latest.commit) {
			slog.DebugContext(ctx, "[controller] rotated resource cleanup postponed: stack is busy",
				slog.String("stack", stack.Name))
		}
	}
}

// finishStageFailure records a run that failed before its stacks were queued. It keeps GitRevision,
// which names the latest revision dispatched to the stack queue: the failed revision was never queued.
func (r *Runner) finishStageFailure(
	ctx context.Context, reason TriggerReason, startedAt time.Time, revision, stage string, err error,
) {
	slog.ErrorContext(ctx, "sync failed at "+stage+" stage",
		slog.String("reason", string(reason)), slog.String("commit", revision), slog.Any("err", err))
	r.metrics.Sync.RecordSyncRun(string(reason), syncRunResultError, time.Since(startedAt))
	_ = r.updateState(ctx, func(state *model.Runtime) {
		state.LastSyncAt, state.LastSyncReason = time.Now(), string(reason)
		state.LastSyncResult, state.LastSyncError = syncRunResultError, err.Error()
	})
}

// dispatchStacks makes every planned stack at commit the complete set of reconciliations waiting in
// the stack queue, changed stacks first. Waiting work of stacks that are no longer configured is
// dropped atomically. It returns errQueueStopped when the queue no longer accepts work.
func (r *Runner) dispatchStacks(
	ctx context.Context, plan stackplan.Plan, commit string, reason TriggerReason,
) error {
	requests := make([]stackloop.ReconciliationRequest, 0, len(plan.Stacks))
	for _, stackCfg := range plan.Stacks {
		requests = append(requests, stackloop.ReconciliationRequest{
			Stack: stackCfg, Commit: commit, IsManual: reason == TriggerManual,
		})
	}

	dropped, accepted := r.stacks.Replace(ctx, requests, plan.Changed)
	if !accepted {
		return errQueueStopped
	}
	r.dispatchMu.Lock()
	r.lastDispatch = &dispatch{commit: commit, stacks: plan.Stacks}
	r.dispatchMu.Unlock()
	if len(dropped) > 0 {
		slog.InfoContext(ctx, "[controller] dropped queued operations of stacks removed from configuration",
			slog.Any("stacks", dropped), slog.String("commit", commit))
	}
	return nil
}

func (r *Runner) resolveGitState(
	ctx context.Context, reason TriggerReason, gitResult *gitx.PullResult,
) (gitx.PullResult, error) {
	if gitResult != nil {
		return *gitResult, nil
	}
	if reason == TriggerInterval {
		revision, err := r.git.Head(ctx)
		if err != nil {
			return gitx.PullResult{}, err
		}
		return gitx.PullResult{OldRevision: revision, NewRevision: revision}, nil
	}
	return r.puller.Pull(ctx, string(reason))
}

func (r *Runner) updatePollState(ctx context.Context, result string, err error) {
	_ = r.updateState(ctx, func(state *model.Runtime) {
		state.LastPollAt, state.LastPollResult, state.LastPollError = time.Now(), result, ""
		if err != nil {
			state.LastPollError = err.Error()
		}
	})
}

func (r *Runner) updateState(ctx context.Context, fn func(*model.Runtime)) error {
	if err := r.stateStore.Update(ctx, fn); err != nil {
		slog.ErrorContext(ctx, "persist controller status failed", "err", err)
		return err
	}
	return nil
}

func (r *Runner) publishManualSync(ctx context.Context, task Task) error {
	if task.Reason != TriggerManual {
		return nil
	}
	return r.event.Publish(ctx, &events.SyncManualStarted{Username: task.TriggeredBy})
}
