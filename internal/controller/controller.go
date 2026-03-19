package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/event"
	"github.com/artarts36/swarm-deploy/internal/gitops"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/notify"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

type TriggerReason string

const (
	TriggerStartup TriggerReason = "startup"
	TriggerPoll    TriggerReason = "poll"
	TriggerWebhook TriggerReason = "webhook"
	TriggerManual  TriggerReason = "manual"
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
	gitSync  *gitops.Syncer
	deployer *swarm.Deployer
	metrics  *metrics.Recorder
	event    *event.Dispatcher

	stateStore      *runtimeStateStore
	stackReconciler *stackReconciler

	triggerCh chan TriggerReason
}

func New(
	cfg *config.Config,
	gitSync *gitops.Syncer,
	deployer *swarm.Deployer,
	metricRecorder *metrics.Recorder,
	notifier *notify.Manager,
) *Controller {
	return &Controller{
		cfg:        cfg,
		gitSync:    gitSync,
		deployer:   deployer,
		metrics:    metricRecorder,
		event:      event.NewDispatcher(notifier),
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

func (c *Controller) syncOnce(ctx context.Context, reason TriggerReason) {
	startedAt := time.Now()

	slog.InfoContext(ctx, "[controller] run sync", slog.String("reason", string(reason)))

	syncResult, err := c.gitSync.Sync(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "sync failed at git stage",
			slog.String("reason", string(reason)),
			slog.String("repository", c.cfg.Spec.Git.Repository),
			slog.Any("err", err),
		)
		c.metrics.RecordGitUpdate(c.cfg.Spec.Git.Repository, "error")
		c.metrics.RecordSyncRun(string(reason), "error", time.Since(startedAt))
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
	c.metrics.RecordGitUpdate(c.cfg.Spec.Git.Repository, updateResult)

	if !syncResult.Updated && reason != TriggerManual {
		c.metrics.RecordSyncRun(string(reason), "no_change", time.Since(startedAt))
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
			slog.Error("sync failed for stack",
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
		slog.Error("sync finished with errors",
			slog.String("reason", string(reason)),
			slog.String("commit", syncResult.NewRevision),
			slog.Any("err", combinedErr),
		)
	}

	c.metrics.RecordSyncRun(string(reason), result, time.Since(startedAt))
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
		c.metrics.RecordDeploy(stackCfg.Name, service.Name, "success")
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

	c.event.DispatchSuccessfulDeploy(event.SuccessfulDeployEvent{
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
		c.metrics.RecordDeploy(stackName, service.Name, "failed")
	}
	if len(servicesState) == 0 {
		c.metrics.RecordDeploy(stackName, "unknown", "failed")
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

	c.event.DispatchFailedDeploy(event.FailedDeployEvent{
		StackName: stackName,
		Commit:    commit,
		Services:  services,
		Error:     reason,
	})
}
