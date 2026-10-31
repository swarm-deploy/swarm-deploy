// Package syncrun executes complete serialized GitOps synchronization runs.
package syncrun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
	syncRunResultError        = "error"
	syncRunResultNoChange     = "no_change"
	syncRunResultUpdated      = "updated"
	syncRunResultSuccess      = "success"
	syncRunResultPartialError = "partial_error"
)

// Task describes one queued synchronization request.
type Task struct {
	TriggeredBy string
	Reason      TriggerReason
	SpanContext trace.SpanContext
}

// DeploymentQueue accepts immutable stack execution inputs.
type DeploymentQueue interface {
	// Enqueue snapshots source data for a stack deployment.
	Enqueue(context.Context, stackloop.ReconciliationRequest, string) error
}

// Runner executes Git polling, complete synchronization runs and scheduled cleanup.
type Runner struct {
	configStore       *configstore.ConfigStore
	git               gitx.Repository
	metrics           *metrics.Group
	event             dispatcher.Dispatcher
	stateStore        modelstore.Store
	networkReconciler *networkloop.Coordinator
	stackReconciler   stackloop.StackReconciler
	deployments       DeploymentQueue
	stackPlanner      *stackplan.Planner
	puller            *gitpull.Puller
	tracer            trace.Tracer
}

// New creates a synchronization runner from the existing GitOps components.
func New(
	configStore *configstore.ConfigStore,
	git gitx.Repository,
	metricGroup *metrics.Group,
	eventDispatcher dispatcher.Dispatcher,
	stateStore modelstore.Store,
	networkReconciler *networkloop.Coordinator,
	stackReconciler stackloop.StackReconciler,
	puller *gitpull.Puller,
	deployments ...DeploymentQueue,
) *Runner {
	var queue DeploymentQueue
	if len(deployments) > 0 {
		queue = deployments[0]
	}
	return &Runner{
		configStore: configStore, git: git, metrics: metricGroup, event: eventDispatcher,
		stateStore: stateStore, networkReconciler: networkReconciler,
		stackReconciler: stackReconciler, stackPlanner: stackplan.NewPlanner(git), puller: puller,
		deployments: queue,
		tracer:      otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"),
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
	span.AddEvent("git pull completed", trace.WithAttributes(attribute.Bool("git.updated", gitResult.Updated)))
	if gitResult.Updated {
		r.run(ctx, Task{Reason: TriggerPoll}, &gitResult)
	}
}

// Run executes one complete GitOps synchronization.
func (r *Runner) Run(ctx context.Context, task Task) {
	r.run(ctx, task, nil)
}

func (r *Runner) run(ctx context.Context, task Task, gitResult *gitx.PullResult) {
	if task.SpanContext.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, task.SpanContext)
	}
	ctx, span := r.tracer.Start(ctx, "controller.Sync", trace.WithAttributes(
		attribute.String("sync.trigger", string(task.Reason)),
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
	if err = r.configStore.Reload(ctx, r.git.WorkingDir()); err != nil {
		r.finishStageFailure(ctx, task.Reason, startedAt, gitResult.NewRevision, "configuration reload", err)
		return
	}
	slog.InfoContext(ctx, "[controller] configuration reloaded",
		slog.Int("stacks", len(r.configStore.Stacks())), slog.Int("networks", len(r.configStore.Networks())))

	if err = r.networkReconciler.Reconcile(ctx, gitResult.NewRevision); err != nil {
		r.finishStageFailure(ctx, task.Reason, startedAt, gitResult.NewRevision, "networks reconcile", err)
		return
	}

	plan := r.stackPlanner.Plan(ctx, r.configStore.Stacks(), *gitResult)
	if plan.DiffErr != nil {
		slog.ErrorContext(ctx, "git diff failed, continue with default stack order",
			slog.String("reason", string(task.Reason)), slog.String("old_revision", gitResult.OldRevision),
			slog.String("new_revision", gitResult.NewRevision), slog.Any("err", plan.DiffErr))
	}

	stackCtx, stackSpan := r.tracer.Start(ctx, "controller.syncStacks", trace.WithAttributes(
		attribute.Int("stacks.total", len(plan.Stacks)), attribute.Int("stacks.changed", len(plan.Changed))))
	defer stackSpan.End()
	deployErrs := r.syncStacks(stackCtx, plan.Stacks, gitResult.NewRevision, task.Reason)

	result := syncRunResultSuccess
	combinedErr := errors.Join(deployErrs...)
	if combinedErr != nil {
		result = syncRunResultPartialError
		slog.ErrorContext(ctx, "sync finished with errors", slog.String("reason", string(task.Reason)),
			slog.String("commit", gitResult.NewRevision), slog.Any("err", combinedErr))
		stackSpan.RecordError(combinedErr)
		stackSpan.SetStatus(codes.Error, combinedErr.Error())
	} else {
		stackSpan.SetStatus(codes.Ok, "")
	}

	persistErr := r.updateState(ctx, func(state *model.Runtime) {
		state.LastSyncAt, state.LastSyncReason, state.LastSyncResult = time.Now(), string(task.Reason), result
		state.LastSyncError = ""
		if combinedErr != nil {
			state.LastSyncError = combinedErr.Error()
		}
		state.GitRevision = gitResult.NewRevision
	})
	if persistErr != nil {
		result = syncRunResultError
		tracing.FailSpan(stackSpan, persistErr)
	}
	r.metrics.Sync.RecordSyncRun(string(task.Reason), result, time.Since(startedAt))
}

// Cleanup reconciles rotated-resource cleanup for every configured stack.
func (r *Runner) Cleanup(ctx context.Context) {
	for _, stack := range r.configStore.Stacks() {
		if err := r.stackReconciler.Cleanup(ctx, stack); err != nil {
			slog.WarnContext(ctx, "[controller] rotated resource cleanup failed",
				slog.String("stack", stack.Name), slog.Any("error", err))
		}
	}
}

func (r *Runner) finishStageFailure(
	ctx context.Context, reason TriggerReason, startedAt time.Time, revision, stage string, err error,
) {
	slog.ErrorContext(ctx, "sync failed at "+stage+" stage",
		slog.String("reason", string(reason)), slog.String("commit", revision), slog.Any("err", err))
	r.metrics.Sync.RecordSyncRun(string(reason), syncRunResultError, time.Since(startedAt))
	_ = r.updateState(ctx, func(state *model.Runtime) {
		state.LastSyncAt, state.LastSyncReason = time.Now(), string(reason)
		state.LastSyncResult, state.LastSyncError, state.GitRevision = syncRunResultError, err.Error(), revision
	})
}

func (r *Runner) syncStacks(
	ctx context.Context, stacks []config.StackSpec, commit string, reason TriggerReason,
) []error {
	var deployErrs []error
	for _, stackCfg := range stacks {
		request := stackloop.ReconciliationRequest{Stack: stackCfg, Commit: commit, IsManual: reason == TriggerManual}
		var err error
		if r.deployments == nil {
			err = r.stackReconciler.Reconcile(ctx, request)
		} else {
			err = r.deployments.Enqueue(ctx, request, r.git.WorkingDir())
		}
		if err == nil {
			continue
		}
		wrapped := fmt.Errorf("stack %s %w", stackCfg.Name, err)
		deployErrs = append(deployErrs, wrapped)
		slog.ErrorContext(ctx, "sync failed for stack", slog.String("reason", string(reason)),
			slog.String("stack", stackCfg.Name), slog.String("commit", commit), slog.Any("err", wrapped))
	}
	return deployErrs
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
