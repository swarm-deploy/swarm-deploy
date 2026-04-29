package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/controller/statem"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
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

type StackView struct {
	Name         string        `json:"name"`
	ComposeFile  string        `json:"compose_file"`
	LastStatus   string        `json:"last_status"`
	LastError    string        `json:"last_error,omitempty"`
	LastCommit   string        `json:"last_commit,omitempty"`
	LastDeployAt time.Time     `json:"last_deploy_at,omitempty"`
	SourceDigest string        `json:"source_digest,omitempty"`
	Services     []ServiceView `json:"services"`
}

type ServiceView struct {
	Name         string    `json:"name"`
	Image        string    `json:"image,omitempty"`
	ImageVersion string    `json:"image_version,omitempty"`
	LastStatus   string    `json:"last_status,omitempty"`
	LastDeployAt time.Time `json:"last_deploy_at,omitempty"`
}

type Controller struct {
	cfg      *config.Config
	git      gitx.Repository
	deployer *deployer.Deployer
	metrics  *metrics.Group
	event    dispatcher.Dispatcher

	stateStore        statem.Store
	networkReconciler *networkReconciler
	stackReconciler   *stackReconciler

	triggerCh chan triggerTask
}

type triggerTask struct {
	triggeredBy string
	reason      TriggerReason
}

func New(
	cfg *config.Config,
	git gitx.Repository,
	networks networkManager,
	deployer *deployer.Deployer,
	metricGroup *metrics.Group,
	eventDispatcher dispatcher.Dispatcher,
	stateStore statem.Store,
) *Controller {
	return &Controller{
		cfg:        cfg,
		git:        git,
		deployer:   deployer,
		metrics:    metricGroup,
		event:      eventDispatcher,
		stateStore: stateStore,
		networkReconciler: newNetworkReconciler(
			networks,
		),
		stackReconciler: newStackReconciler(
			cfg,
			git,
			deployer,
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
		c.updateState(func(s *statem.Runtime) {
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
		c.stateStore.Update(func(s *statem.Runtime) {
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
		c.stateStore.Update(func(s *statem.Runtime) {
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
		c.updateState(func(s *statem.Runtime) {
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

	if !syncResult.Updated && task.reason != TriggerManual {
		c.metrics.Sync.RecordSyncRun(string(task.reason), syncRunResultNoChange, time.Since(startedAt))
		currTime := time.Now()
		c.updateState(func(s *statem.Runtime) {
			s.LastSyncAt = currTime
			s.LastSyncReason = string(task.reason)
			s.LastSyncResult = syncRunResultNoChange
			s.LastSyncError = ""
			s.GitRevision = syncResult.NewRevision
		})
		return
	}

	var deployErrs []error
	for _, stackCfg := range c.cfg.Spec.Stacks {
		err = c.syncStack(ctx, stackCfg, syncResult.NewRevision)
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
	c.updateState(func(s *statem.Runtime) {
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

func (c *Controller) syncStack(ctx context.Context, stackCfg config.StackSpec, commit string) error {
	currentState := c.stateStore.Get()
	prev, exists := currentState.Stacks[stackCfg.Name]
	reconcileResult, err := c.stackReconciler.Reconcile(ctx, stackCfg, prev.SourceDigest, exists)
	if err != nil {
		c.recordStackFailure(stackCfg.Name, commit, failedServicesFromReconcileError(err), err)
		return fmt.Errorf("stack %s %w", stackCfg.Name, err)
	}
	if reconcileResult.Skipped {
		return nil
	}

	now := time.Now()
	servicesState := map[string]statem.Service{}
	for _, service := range reconcileResult.Services {
		servicesState[service.Name] = statem.Service{
			Image:        service.Image,
			LastStatus:   "success",
			LastDeployAt: now,
		}
		c.metrics.Deploys.RecordDeploy(stackCfg.Name, service.Name, "success")
	}

	c.updateState(func(s *statem.Runtime) {
		s.Stacks[stackCfg.Name] = statem.Stack{
			SourceDigest: reconcileResult.SourceDigest,
			LastCommit:   commit,
			LastStatus:   "success",
			LastError:    "",
			LastDeployAt: now,
			Services:     servicesState,
		}
	})

	c.event.Dispatch(ctx, &events.DeploySuccess{
		StackName: stackCfg.Name,
		Commit:    commit,
		Services:  reconcileResult.Services,
	})
	return nil
}

func (c *Controller) recordStackFailure(stackName, commit string, services []compose.Service, reason error) {
	now := time.Now()
	servicesState := map[string]statem.Service{}
	for _, service := range services {
		servicesState[service.Name] = statem.Service{
			Image:        service.Image,
			LastStatus:   "failed",
			LastDeployAt: now,
		}
		c.metrics.Deploys.RecordDeploy(stackName, service.Name, "failed")
	}
	if len(servicesState) == 0 {
		c.metrics.Deploys.RecordDeploy(stackName, "unknown", "failed")
	}

	c.updateState(func(s *statem.Runtime) {
		s.Stacks[stackName] = statem.Stack{
			SourceDigest: "",
			LastCommit:   commit,
			LastStatus:   "failed",
			LastError:    reason.Error(),
			LastDeployAt: now,
			Services:     servicesState,
		}
	})

	logs := []string{}

	var logsErr containsLogsError
	if errors.As(reason, &logsErr) {
		logs = logsErr.Logs()
	}

	c.event.Dispatch(context.Background(), &events.DeployFailed{
		StackName: stackName,
		Commit:    commit,
		Services:  services,
		Error:     reason,
		Logs:      logs,
	})
}

type containsLogsError interface {
	Logs() []string
}
