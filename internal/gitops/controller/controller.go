package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller/stackloop"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type TriggerReason string

const (
	TriggerStartup TriggerReason = "startup"
	TriggerPoll    TriggerReason = "poll"
	TriggerWebhook TriggerReason = "webhook"
	TriggerManual  TriggerReason = "manual"
)

const (
	eventShutdownTimeout = 5 * time.Second

	syncRunResultError        = "error"
	syncRunResultNoChange     = "no_change"
	syncRunResultUpdated      = "updated"
	syncRunResultSuccess      = "success"
	syncRunResultPartialError = "partial_error"
)

type Controller struct {
	cfg      *config.Config
	git      gitx.Repository
	deployer *deployer.Deployer
	metrics  *metrics.Group
	event    dispatcher.Dispatcher

	stateStore        modelstore.Store
	networkReconciler *networkReconciler
	stackReconciler   *stackloop.Reconciler

	triggerCh chan triggerTask
}

type triggerTask struct {
	triggeredBy string
	reason      TriggerReason
}

func New(
	cfg *config.Config,
	git gitx.Repository,
	swarmService *swarm.Swarm,
	deployer *deployer.Deployer,
	metricGroup *metrics.Group,
	eventDispatcher dispatcher.Dispatcher,
	stateStore modelstore.Store,
) *Controller {
	return &Controller{
		cfg:        cfg,
		git:        git,
		deployer:   deployer,
		metrics:    metricGroup,
		event:      eventDispatcher,
		stateStore: stateStore,
		networkReconciler: newNetworkReconciler(
			swarmService.Networks,
		),
		stackReconciler: stackloop.New(
			cfg,
			git,
			deployer,
			swarmService,
			eventDispatcher,
			metricGroup.Deploys,
			stateStore,
		),
		triggerCh: make(chan triggerTask, 1),
	}
}

func (c *Controller) Run(ctx context.Context) error {
	var ticker *time.Ticker
	if c.cfg.Spec.Sync.Mode == config.SyncModePull || c.cfg.Spec.Sync.Mode == config.SyncModeHybrid {
		ticker = time.NewTicker(c.cfg.Spec.Sync.PollInterval.Value)
		defer ticker.Stop()
	}

	slog.InfoContext(ctx, "[controller] trigger startup sync")

	c.trigger(triggerTask{
		reason: TriggerStartup,
	})

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), eventShutdownTimeout)
			if err := c.event.Shutdown(shutdownCtx); err != nil {
				slog.ErrorContext(
					context.Background(),
					"[controller] failed to shutdown event dispatcher",
					slog.Any("err", err),
				)
			}
			cancel()
			return nil
		case task := <-c.triggerCh:
			c.syncOnce(ctx, task)
		case <-tickerC(ticker):
			c.trigger(triggerTask{
				reason: TriggerPoll,
			})
		}
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

	return c.trigger(triggerTask{
		triggeredBy: user.Name,
		reason:      TriggerManual,
	})
}

func (c *Controller) Webhook() bool {
	return c.trigger(triggerTask{
		reason: TriggerWebhook,
	})
}

func (c *Controller) trigger(task triggerTask) bool {
	select {
	case c.triggerCh <- task:
		return true
	default:
		return false
	}
}

func (c *Controller) syncOnce(ctx context.Context, task triggerTask) { //nolint:funlen // not need
	startedAt := time.Now()

	slog.InfoContext(ctx, "[controller] run sync", slog.String("reason", string(task.reason)))
	if task.reason == TriggerManual {
		c.event.Dispatch(ctx, &events.SyncManualStarted{
			TriggeredBy: task.triggeredBy,
		})
	}

	syncResult, err := c.git.Pull(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "sync failed at git stage",
			slog.String("reason", string(task.reason)),
			slog.String("repository", c.cfg.Spec.Git.Repository),
			slog.Any("err", err),
		)
		c.metrics.Git.RecordGitUpdate(c.cfg.Spec.Git.Repository, "error")
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultError, time.Since(startedAt))
		c.updateState(func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = err.Error()
		})
		return
	}

	slog.InfoContext(ctx, "[controller] git synced", slog.Any("result", syncResult))

	updateResult := syncRunResultNoChange
	if syncResult.Updated {
		updateResult = syncRunResultUpdated
	}
	c.metrics.Git.RecordGitUpdate(c.cfg.Spec.Git.Repository, updateResult)

	reloadedNetworksFrom, reloadNetworksErr := c.reloadNetworks()
	if reloadNetworksErr != nil {
		slog.ErrorContext(ctx, "sync failed at networks reload stage",
			slog.String("reason", string(task.reason)),
			slog.String("networks.file", c.cfg.Spec.NetworksSource.File),
			slog.Any("err", reloadNetworksErr),
		)
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultError, time.Since(startedAt))
		c.stateStore.Update(func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = reloadNetworksErr.Error()
			s.GitRevision = syncResult.NewRevision
		})
		return
	}
	if reloadedNetworksFrom != "" {
		slog.InfoContext(ctx, "[controller] networks reloaded",
			slog.String("path", reloadedNetworksFrom),
			slog.Int("count", len(c.cfg.Spec.Networks)),
		)
	}

	reconcileNetworksErr := c.syncNetworks(ctx, syncResult.NewRevision)
	if reconcileNetworksErr != nil {
		slog.ErrorContext(ctx, "sync failed at networks reconcile stage",
			slog.String("reason", string(task.reason)),
			slog.String("commit", syncResult.NewRevision),
			slog.Any("err", reconcileNetworksErr),
		)
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultError, time.Since(startedAt))
		c.stateStore.Update(func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = reconcileNetworksErr.Error()
			s.GitRevision = syncResult.NewRevision
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
		c.updateState(func(s *model.Runtime) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultError
			s.LastSyncError = reloadErr.Error()
			s.GitRevision = syncResult.NewRevision
		})
		return
	}

	slog.InfoContext(ctx, "[controller] stacks reloaded",
		slog.String("path", reloadedFrom),
		slog.Int("count", len(c.cfg.Spec.Stacks)),
	)

	stacksToSync := c.cfg.Spec.Stacks
	if syncResult.Updated {
		fileDiffs, diffErr := c.git.Diff(ctx, syncResult.OldRevision, syncResult.NewRevision)
		if diffErr != nil {
			slog.ErrorContext(ctx, "git diff failed, continue with default stack order",
				slog.String("reason", string(task.reason)),
				slog.String("old_revision", syncResult.OldRevision),
				slog.String("new_revision", syncResult.NewRevision),
				slog.Any("err", diffErr),
			)
		} else {
			stacksToSync = prioritizeStacksByFileDiffs(c.cfg.Spec.Stacks, fileDiffs)
		}
	}

	var deployErrs []error
	for _, stackCfg := range stacksToSync {
		err = c.syncStack(ctx, stackCfg, syncResult.NewRevision, task.reason == TriggerManual)
		if err != nil {
			deployErrs = append(deployErrs, err)
			slog.ErrorContext(ctx, "sync failed for stack",
				slog.String("reason", string(task.reason)),
				slog.String("stack", stackCfg.Name),
				slog.String("commit", syncResult.NewRevision),
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
			slog.String("commit", syncResult.NewRevision),
			slog.Any("err", combinedErr),
		)
	}

	c.metrics.Sync.RecordSyncRun(string(task.reason), result, time.Since(startedAt))
	c.updateState(func(s *model.Runtime) {
		s.LastSyncAt = time.Now()
		s.LastSyncReason = string(task.reason)
		s.LastSyncResult = result
		s.LastSyncError = ""
		if combinedErr != nil {
			s.LastSyncError = combinedErr.Error()
		}
		s.GitRevision = syncResult.NewRevision
	})
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
