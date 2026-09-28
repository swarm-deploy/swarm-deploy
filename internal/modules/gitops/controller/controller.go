package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TriggerReason string

const (
	TriggerStartup  TriggerReason = "startup"
	TriggerPoll     TriggerReason = "poll"
	TriggerInterval TriggerReason = "interval"
	TriggerWebhook  TriggerReason = "webhook"
	TriggerManual   TriggerReason = "manual"
)

const (
	syncRunResultError        = "error"
	syncRunResultNoChange     = "no_change"
	syncRunResultUpdated      = "updated"
	syncRunResultSuccess      = "success"
	syncRunResultPartialError = "partial_error"
)

type Controller struct {
	cfg      *config.Config
	git      gitx.Repository
	deployer deployer.StackDeployer
	metrics  *metrics.Group
	event    dispatcher.Dispatcher

	stateStore        modelstore.Store
	networkReconciler *networkloop.Reconciler
	stackReconciler   stackloop.StackReconciler

	reconcileCh chan reconcileTask

	shuttingDown atomic.Bool
	tickerMu     sync.Mutex
	tickers      []*time.Ticker

	tracer trace.Tracer
}

type reconcileTask struct {
	triggeredBy string
	reason      TriggerReason
	spanContext trace.SpanContext
}

func New(
	cfg *config.Config,
	git gitx.Repository,
	swarmService *swarm.Swarm,
	deployer deployer.StackDeployer,
	metricGroup *metrics.Group,
	eventDispatcher dispatcher.Dispatcher,
	stateStore modelstore.Store,
	filesystem fs.FileSystem,
) *Controller {
	return &Controller{
		cfg:        cfg,
		git:        git,
		deployer:   deployer,
		metrics:    metricGroup,
		event:      eventDispatcher,
		stateStore: stateStore,
		networkReconciler: networkloop.New(
			swarmService.Networks,
			eventDispatcher,
		),
		stackReconciler: stackloop.NewStackReconciler(
			cfg,
			git,
			deployer,
			swarmService,
			eventDispatcher,
			metricGroup.Deploys,
			stateStore,
			filesystem,
		),
		reconcileCh: make(chan reconcileTask, 1),
		tracer:      otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"),
	}
}

func (c *Controller) Run(ctx context.Context) error {
	runDone := make(chan struct{})
	defer close(runDone)
	go func() {
		select {
		case <-ctx.Done():
			c.requestShutdown()
		case <-runDone:
		}
	}()

	var pollTicker *time.Ticker
	if c.cfg.Spec.Sync.Mode == config.SyncModePull || c.cfg.Spec.Sync.Mode == config.SyncModeHybrid {
		pollTicker = time.NewTicker(c.cfg.Spec.Sync.PollInterval.Value)
		c.addTicker(pollTicker)
		defer c.removeTicker(pollTicker)
	}

	reconcileTicker := time.NewTicker(c.cfg.Spec.Sync.Interval.Value)
	c.addTicker(reconcileTicker)
	defer c.removeTicker(reconcileTicker)

	slog.InfoContext(ctx, "[controller] trigger startup sync")

	c.scheduleReconcile(ctx, reconcileTask{
		reason: TriggerStartup,
	})

	reconciliationCtx := context.WithoutCancel(ctx)

	for {
		if c.shuttingDown.Load() {
			return nil
		}

		select {
		case <-ctx.Done():
			c.requestShutdown()
			return nil
		case task := <-c.reconcileCh:
			if c.shuttingDown.Load() {
				continue
			}
			c.reconcile(reconciliationCtx, task, nil)
		case <-tickerC(pollTicker):
			if c.shuttingDown.Load() {
				continue
			}
			c.pollGit(reconciliationCtx)
		case <-reconcileTicker.C:
			if c.shuttingDown.Load() {
				continue
			}
			c.scheduleReconcile(ctx, reconcileTask{
				reason: TriggerInterval,
			})
		}
	}
}

func (c *Controller) requestShutdown() {
	c.shuttingDown.Store(true)
	c.stopTicker()
}

func (c *Controller) addTicker(ticker *time.Ticker) {
	c.tickerMu.Lock()
	defer c.tickerMu.Unlock()

	c.tickers = append(c.tickers, ticker)
	if c.shuttingDown.Load() {
		ticker.Stop()
	}
}

func (c *Controller) removeTicker(ticker *time.Ticker) {
	c.tickerMu.Lock()
	defer c.tickerMu.Unlock()

	ticker.Stop()
	for i, registeredTicker := range c.tickers {
		if registeredTicker != ticker {
			continue
		}
		c.tickers = append(c.tickers[:i], c.tickers[i+1:]...)
		return
	}
}

func (c *Controller) stopTicker() {
	c.tickerMu.Lock()
	defer c.tickerMu.Unlock()

	for _, ticker := range c.tickers {
		ticker.Stop()
	}
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func (c *Controller) Manual(ctx context.Context) bool {
	user, _ := security.UserFromContext(ctx)

	return c.scheduleReconcile(ctx, reconcileTask{
		triggeredBy: user.Name,
		reason:      TriggerManual,
	})
}

func (c *Controller) Webhook(ctx context.Context) bool {
	return c.scheduleReconcile(ctx, reconcileTask{
		reason: TriggerWebhook,
	})
}

func (c *Controller) scheduleReconcile(ctx context.Context, task reconcileTask) bool {
	if c.shuttingDown.Load() {
		return false
	}

	task.spanContext = trace.SpanContextFromContext(ctx)

	select {
	case c.reconcileCh <- task:
		return true
	default:
		return false
	}
}

func (c *Controller) pollGit(ctx context.Context) {
	ctx, span := c.tracer.Start(
		ctx,
		"controller.Poll",
		trace.WithAttributes(tracing.SyncReason.String(string(TriggerPoll))),
	)
	defer span.End()

	gitResult, err := c.pullGit(ctx, TriggerPoll)
	if err != nil {
		tracing.FailSpan(span, err)
		c.updatePollState(ctx, syncRunResultError, err)
		return
	}

	pollResult := syncRunResultNoChange
	if gitResult.Updated {
		pollResult = syncRunResultUpdated
	}
	c.updatePollState(ctx, pollResult, nil)

	span.AddEvent(
		"git pull completed",
		trace.WithAttributes(
			attribute.Bool("git.updated", gitResult.Updated),
		),
	)

	if !gitResult.Updated {
		return
	}

	c.reconcile(ctx, reconcileTask{reason: TriggerPoll}, &gitResult)
}

func (c *Controller) reconcile(
	ctx context.Context,
	task reconcileTask,
	gitResult *gitx.PullResult,
) { //nolint:funlen // reconciliation pipeline
	ctx = trace.ContextWithSpanContext(ctx, task.spanContext)
	ctx, span := c.tracer.Start(
		ctx,
		"controller.Sync",
		trace.WithAttributes(attribute.String("sync.trigger", string(task.reason))),
	)
	defer span.End()

	startedAt := time.Now()

	if task.reason == TriggerManual {
		c.event.Dispatch(ctx, &events.SyncManualStarted{
			TriggeredBy: task.triggeredBy,
		})
	}

	resolvedGitResult, err := c.resolveGitState(ctx, task.reason, gitResult)
	if err != nil {
		tracing.FailSpan(span, err)
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultError, time.Since(startedAt))
		c.updateState(ctx, func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = err.Error()
		})
		return
	}
	gitResult = &resolvedGitResult

	slog.InfoContext(ctx, "[controller] run sync", slog.String("reason", string(task.reason)))

	reloadedNetworksFrom, reloadNetworksErr := c.reloadNetworks()
	if reloadNetworksErr != nil {
		slog.ErrorContext(ctx, "sync failed at networks reload stage",
			slog.String("reason", string(task.reason)),
			slog.String("networks.file", c.cfg.Spec.NetworksSource.File),
			slog.Any("err", reloadNetworksErr),
		)
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultError, time.Since(startedAt))
		c.stateStore.Update(ctx, func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = reloadNetworksErr.Error()
			s.GitRevision = gitResult.NewRevision
		})
		return
	}
	if reloadedNetworksFrom != "" {
		slog.InfoContext(ctx, "[controller] networks reloaded",
			slog.String("path", reloadedNetworksFrom),
			slog.Int("count", len(c.cfg.Spec.Networks)),
		)
	}

	reconcileNetworksErr := c.syncNetworks(ctx, gitResult.NewRevision)
	if reconcileNetworksErr != nil {
		slog.ErrorContext(ctx, "sync failed at networks reconcile stage",
			slog.String("reason", string(task.reason)),
			slog.String("commit", gitResult.NewRevision),
			slog.Any("err", reconcileNetworksErr),
		)
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultError, time.Since(startedAt))
		c.stateStore.Update(ctx, func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = reconcileNetworksErr.Error()
			s.GitRevision = gitResult.NewRevision
		})
		return
	}

	reloadedFrom, reloadErr := c.reloadStacks()
	if reloadErr != nil {
		slog.ErrorContext(ctx, "sync failed at stacks reload stage",
			slog.String("reason", string(task.reason)),
			slog.String("stacks.file", c.cfg.Spec.StacksSource.File),
			slog.Any("err", reloadErr),
		)
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultError, time.Since(startedAt))
		c.updateState(ctx, func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = reloadErr.Error()
			s.GitRevision = gitResult.NewRevision
		})
		return
	}

	slog.InfoContext(ctx, "[controller] stacks reloaded",
		slog.String("path", reloadedFrom),
		slog.Int("count", len(c.cfg.Spec.Stacks)),
	)

	stacksToSync := c.cfg.Spec.Stacks
	if gitResult.Updated {
		fileDiffs, diffErr := c.git.Diff(ctx, gitResult.OldRevision, gitResult.NewRevision)
		if diffErr != nil {
			slog.ErrorContext(ctx, "git diff failed, continue with default stack order",
				slog.String("reason", string(task.reason)),
				slog.String("old_revision", gitResult.OldRevision),
				slog.String("new_revision", gitResult.NewRevision),
				slog.Any("err", diffErr),
			)
		} else {
			stacksToSync = prioritizeStacksByFileDiffs(c.cfg.Spec.Stacks, fileDiffs)
		}
	}

	var deployErrs []error

	stackCtx, stackSpan := c.tracer.Start(ctx, "controller.syncStacks")
	defer stackSpan.End()

	for _, stackCfg := range stacksToSync {
		err = c.syncStack(stackCtx, stackCfg, gitResult.NewRevision, task.reason == TriggerManual)
		if err != nil {
			deployErrs = append(deployErrs, err)
			slog.ErrorContext(ctx, "sync failed for stack",
				slog.String("reason", string(task.reason)),
				slog.String("stack", stackCfg.Name),
				slog.String("commit", gitResult.NewRevision),
				slog.Any("err", err),
			)
		}
	}

	result := syncRunResultSuccess
	combinedErr := errors.Join(deployErrs...)
	if combinedErr != nil {
		result = syncRunResultPartialError
		slog.ErrorContext(ctx, "sync finished with errors",
			slog.String("reason", string(task.reason)),
			slog.String("commit", gitResult.NewRevision),
			slog.Any("err", combinedErr),
		)

		stackSpan.RecordError(combinedErr)
		stackSpan.SetStatus(codes.Error, combinedErr.Error())
	} else {
		stackSpan.SetStatus(codes.Ok, "")
	}

	c.metrics.Sync.RecordSyncRun(string(task.reason), result, time.Since(startedAt))
	c.updateState(ctx, func(s *model.Runtime) {
		s.LastSyncAt = time.Now()
		s.LastSyncReason = string(task.reason)
		s.LastSyncResult = result
		s.LastSyncError = ""
		if combinedErr != nil {
			s.LastSyncError = combinedErr.Error()
		}
		s.GitRevision = gitResult.NewRevision
	})
}

func (c *Controller) resolveGitState(
	ctx context.Context,
	reason TriggerReason,
	gitResult *gitx.PullResult,
) (gitx.PullResult, error) {
	if gitResult != nil {
		return *gitResult, nil
	}

	if reason == TriggerInterval {
		revision, err := c.git.Head(ctx)
		if err != nil {
			return gitx.PullResult{}, err
		}

		return gitx.PullResult{
			OldRevision: revision,
			NewRevision: revision,
		}, nil
	}

	return c.pullGit(ctx, reason)
}

func (c *Controller) updatePollState(ctx context.Context, result string, err error) {
	c.updateState(ctx, func(s *model.Runtime) {
		s.LastPollAt = time.Now()
		s.LastPollResult = result
		s.LastPollError = ""
		if err != nil {
			s.LastPollError = err.Error()
		}
	})
}

func (c *Controller) pullGit(ctx context.Context, reason TriggerReason) (gitx.PullResult, error) {
	gitResult, err := c.git.Pull(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "git pull failed",
			slog.String("reason", string(reason)),
			slog.String("repository", c.cfg.Spec.Git.Repository),
			slog.Any("err", err),
		)
		c.metrics.Git.RecordGitUpdate(c.cfg.Spec.Git.Repository, "error")

		return gitx.PullResult{}, err
	}

	slog.InfoContext(ctx, "[controller] git synced", slog.Any("result", gitResult))

	updateResult := syncRunResultNoChange
	if gitResult.Updated {
		updateResult = syncRunResultUpdated
	}
	c.metrics.Git.RecordGitUpdate(c.cfg.Spec.Git.Repository, updateResult)

	return gitResult, nil
}

func (c *Controller) reloadStacks() (string, error) {
	return c.cfg.ReloadStacks(c.git.WorkingDir())
}

func (c *Controller) syncStack(
	ctx context.Context,
	stackCfg config.StackSpec,
	commit string,
	isManual bool,
) error {
	err := c.stackReconciler.Reconcile(ctx, stackloop.ReconciliationRequest{
		Stack:    stackCfg,
		Commit:   commit,
		IsManual: isManual,
	})
	if err != nil {
		return fmt.Errorf("stack %s %w", stackCfg.Name, err)
	}
	return nil
}

func prioritizeStacksByFileDiffs(stacks []config.StackSpec, fileDiffs []gitx.CommitFileDiff) []config.StackSpec {
	if len(stacks) == 0 {
		return nil
	}

	normalizePath := func(path string) string {
		return strings.TrimPrefix(strings.TrimSpace(path), "./")
	}

	stackNameByComposePath := make(map[string]string, len(stacks))
	for _, stack := range stacks {
		composePath := normalizePath(stack.ComposeFile)
		if composePath == "" {
			continue
		}

		if _, exists := stackNameByComposePath[composePath]; !exists {
			stackNameByComposePath[composePath] = stack.Name
		}
	}

	changedStacksOrder := make(map[string]int, len(stacks))
	for diffIndex, fileDiff := range fileDiffs {
		changedPath := normalizePath(fileDiff.NewPath)
		if changedPath == "" {
			changedPath = normalizePath(fileDiff.OldPath)
		}
		if changedPath == "" {
			continue
		}

		stackName, exists := stackNameByComposePath[changedPath]
		if !exists {
			continue
		}
		changedStacksOrder[stackName] = diffIndex
	}

	if len(changedStacksOrder) == 0 {
		return stacks
	}

	orderedStacks := make([]config.StackSpec, len(stacks))
	copy(orderedStacks, stacks)

	sort.SliceStable(orderedStacks, func(i, j int) bool {
		leftOrder, leftChanged := changedStacksOrder[orderedStacks[i].Name]
		rightOrder, rightChanged := changedStacksOrder[orderedStacks[j].Name]

		switch {
		case leftChanged && rightChanged:
			return leftOrder < rightOrder
		case leftChanged:
			return true
		case rightChanged:
			return false
		default:
			return false
		}
	})

	return orderedStacks
}
