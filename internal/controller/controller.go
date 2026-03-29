package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/gitops"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

type TriggerReason string

const (
	TriggerStartup TriggerReason = "startup"
	TriggerPoll    TriggerReason = "poll"
	TriggerWebhook TriggerReason = "webhook"
	TriggerManual  TriggerReason = "manual"
)

const eventShutdownTimeout = 5 * time.Second

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
	gitSync  *gitops.Syncer
	deployer *swarm.Deployer
	metrics  *metrics.Group
	event    dispatcher.Dispatcher

	stateStore      *runtimeStateStore
	stackReconciler *stackReconciler

	triggerCh chan TriggerReason
}

func New(
	cfg *config.Config,
	gitSync *gitops.Syncer,
	deployer *swarm.Deployer,
	metricGroup *metrics.Group,
	eventDispatcher dispatcher.Dispatcher,
) *Controller {
	return &Controller{
		cfg:        cfg,
		gitSync:    gitSync,
		deployer:   deployer,
		metrics:    metricGroup,
		event:      eventDispatcher,
		stateStore: newRuntimeStateStore(),
		stackReconciler: newStackReconciler(
			cfg,
			gitSync,
			deployer,
		),
		triggerCh: make(chan TriggerReason, 1),
	}
}

func (c *Controller) Run(ctx context.Context) error {
	var ticker *time.Ticker
	if c.cfg.Spec.Sync.Mode == config.SyncModePull || c.cfg.Spec.Sync.Mode == config.SyncModeHybrid {
		ticker = time.NewTicker(c.cfg.Spec.Sync.PollInterval.Value)
		defer ticker.Stop()
	}

	slog.InfoContext(ctx, "[controller] trigger startup sync")

	c.Trigger(TriggerStartup)

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
		case reason := <-c.triggerCh:
			c.syncOnce(ctx, reason)
		case <-tickerC(ticker):
			c.Trigger(TriggerPoll)
		}
	}
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func (c *Controller) Trigger(reason TriggerReason) bool {
	select {
	case c.triggerCh <- reason:
		return true
	default:
		return false
	}
}

func (c *Controller) syncOnce(ctx context.Context, reason TriggerReason) { //nolint:funlen // not need
	startedAt := time.Now()

	slog.InfoContext(ctx, "[controller] run sync", slog.String("reason", string(reason)))
	if reason == TriggerManual {
		c.event.Dispatch(ctx, &events.SyncManualStarted{})
	}

	syncResult, err := c.gitSync.Sync(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "sync failed at git stage",
			slog.String("reason", string(reason)),
			slog.String("repository", c.cfg.Spec.Git.Pull.Repository),
			slog.Any("err", err),
		)
		c.metrics.Git.RecordGitUpdate(c.cfg.Spec.Git.Pull.Repository, "error")
		c.metrics.Sync.RecordSyncRun(string(reason), "error", time.Since(startedAt))
		c.updateState(func(s *runtimeState) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(reason)
			s.LastSyncResult = "error"
			s.LastSyncError = err.Error()
		})
		return
	}

	slog.InfoContext(ctx, "[controller] git synced", slog.Any("result", syncResult))

	updateResult := "no_change"
	if syncResult.Updated {
		updateResult = "updated"
	}
	c.metrics.Git.RecordGitUpdate(c.cfg.Spec.Git.Pull.Repository, updateResult)

	reloadedFrom, reloadErr := c.reloadStacks()
	if reloadErr != nil {
		slog.ErrorContext(ctx, "sync failed at stacks reload stage",
			slog.String("reason", string(reason)),
			slog.String("stacks.file", c.cfg.Spec.StacksSource.File),
			slog.Any("err", reloadErr),
		)
		c.metrics.Sync.RecordSyncRun(string(reason), "error", time.Since(startedAt))
		c.updateState(func(s *runtimeState) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(reason)
			s.LastSyncResult = "error"
			s.LastSyncError = reloadErr.Error()
			s.GitRevision = syncResult.NewRevision
		})
		return
	}

	slog.InfoContext(ctx, "[controller] stacks reloaded",
		slog.String("path", reloadedFrom),
		slog.Int("count", len(c.cfg.Spec.Stacks)),
	)

	if !syncResult.Updated && reason != TriggerManual {
		c.metrics.Sync.RecordSyncRun(string(reason), "no_change", time.Since(startedAt))
		c.updateState(func(s *runtimeState) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(reason)
			s.LastSyncResult = "no_change"
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
				slog.String("reason", string(reason)),
				slog.String("stack", stackCfg.Name),
				slog.String("commit", syncResult.NewRevision),
				slog.Any("err", err),
			)
		}
	}

	result := "success"
	combinedErr := errors.Join(deployErrs...)
	if combinedErr != nil {
		result = "partial_error"
		slog.ErrorContext(ctx, "sync finished with errors",
			slog.String("reason", string(reason)),
			slog.String("commit", syncResult.NewRevision),
			slog.Any("err", combinedErr),
		)
	}

	c.metrics.Sync.RecordSyncRun(string(reason), result, time.Since(startedAt))
	c.updateState(func(s *runtimeState) {
		s.LastSyncAt = time.Now()
		s.LastSyncReason = string(reason)
		s.LastSyncResult = result
		s.LastSyncError = ""
		if combinedErr != nil {
			s.LastSyncError = combinedErr.Error()
		}
		s.GitRevision = syncResult.NewRevision
	})
}

func (c *Controller) reloadStacks() (string, error) {
	return c.cfg.ReloadStacks(c.gitSync.WorkingDir())
}

func (c *Controller) syncStack(ctx context.Context, stackCfg config.StackSpec, commit string) error {
	currentState := c.snapshotState()
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
	servicesState := map[string]serviceState{}
	for _, service := range reconcileResult.Services {
		servicesState[service.Name] = serviceState{
			Image:        service.Image,
			LastStatus:   "success",
			LastDeployAt: now,
		}
		c.metrics.Deploys.RecordDeploy(stackCfg.Name, service.Name, "success")
	}

	c.updateState(func(s *runtimeState) {
		s.Stacks[stackCfg.Name] = stackState{
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
	servicesState := map[string]serviceState{}
	for _, service := range services {
		servicesState[service.Name] = serviceState{
			Image:        service.Image,
			LastStatus:   "failed",
			LastDeployAt: now,
		}
		c.metrics.Deploys.RecordDeploy(stackName, service.Name, "failed")
	}
	if len(servicesState) == 0 {
		c.metrics.Deploys.RecordDeploy(stackName, "unknown", "failed")
	}

	c.updateState(func(s *runtimeState) {
		s.Stacks[stackName] = stackState{
			SourceDigest: "",
			LastCommit:   commit,
			LastStatus:   "failed",
			LastError:    reason.Error(),
			LastDeployAt: now,
			Services:     servicesState,
		}
	})

	c.event.Dispatch(context.Background(), &events.DeployFailed{
		StackName: stackName,
		Commit:    commit,
		Services:  services,
		Error:     reason,
	})
}
