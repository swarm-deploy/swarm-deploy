package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/gitpull"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/scheduler"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const gracefulReconciliationShutdownTimeout = 25 * time.Second

type TriggerReason = syncrun.TriggerReason

const (
	TriggerStartup  = syncrun.TriggerStartup
	TriggerPoll     = syncrun.TriggerPoll
	TriggerInterval = syncrun.TriggerInterval
	TriggerWebhook  = syncrun.TriggerWebhook
	TriggerManual   = syncrun.TriggerManual
)

type renderedCleaner interface {
	CleanupStale(ctx context.Context) error
}

// Controller owns GitOps lifecycle, process ownership and graceful shutdown.
type Controller struct {
	deployments  deployments.Executor
	scheduler    *scheduler.Scheduler
	cleaner      renderedCleaner
	ownership    *controllerOwnership
	shutdownWait time.Duration
}

// New assembles the focused GitOps controller components while preserving external entry points.
func New(
	cfg *config.Config,
	configStore *configstore.ConfigStore,
	git gitx.Repository,
	swarmService *swarm.Swarm,
	deploymentExecutor deployments.Executor,
	metricGroup *metrics.Group,
	eventDispatcher dispatcher.Dispatcher,
	stateStore modelstore.Store,
	filesystem fs.FileSystem,
	db storage.Database,
) *Controller {
	rotator := stackloop.NewRotator(filesystem)
	preparer := preparation.New(cfg, git, filesystem, rotator)
	stackReconciler := stackloop.NewStackReconciler(
		cfg, deploymentExecutor, swarmService, eventDispatcher, metricGroup.Deploys,
		stateStore, preparer, rotator, db,
	)
	puller := gitpull.NewPuller(
		git, gitpull.NewMonitor(db, stateStore, eventDispatcher), metricGroup.Git, cfg.Spec.Git,
	)
	runner := syncrun.New(
		db, configStore, git, metricGroup, eventDispatcher, stateStore,
		networkloop.New(swarmService.Networks), stackReconciler, puller,
	)
	return &Controller{
		deployments:  deploymentExecutor,
		scheduler:    scheduler.New(cfg, runner, eventDispatcher),
		cleaner:      preparer,
		ownership:    newControllerOwnership(cfg.Spec.DataDir),
		shutdownWait: gracefulReconciliationShutdownTimeout,
	}
}

// Run acquires controller ownership, starts components and waits for graceful shutdown.
func (c *Controller) Run(ctx context.Context) error {
	runDone := make(chan struct{})
	defer close(runDone)
	reconciliationCtx, cancelReconciliation := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelReconciliation()
	shutdownWait := c.shutdownWait
	if shutdownWait <= 0 {
		shutdownWait = gracefulReconciliationShutdownTimeout
	}
	c.watchShutdown(ctx, runDone, cancelReconciliation, shutdownWait)

	var handle *ownershipHandle
	if c.ownership != nil {
		var err error
		handle, err = c.acquireOwnership(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("acquire controller ownership: %w", err)
		}
	}
	if handle != nil {
		defer c.releaseOwnership(ctx, handle)
		if c.cleaner != nil {
			if err := c.cleaner.CleanupStale(ctx); err != nil {
				slog.WarnContext(ctx, "failed to clean up rendered compose files", "err", err)
			}
		}
	}
	if c.deployments != nil {
		if err := c.deployments.Recover(ctx); err != nil {
			return fmt.Errorf("recover orphaned deployments: %w", err)
		}
	}
	return c.scheduler.Run(ctx, reconciliationCtx)
}

// Manual requests a non-blocking manual synchronization.
func (c *Controller) Manual(ctx context.Context) bool {
	return c.scheduler.Manual(ctx)
}

// Webhook requests a non-blocking webhook synchronization.
func (c *Controller) Webhook(ctx context.Context) (bool, error) {
	return c.scheduler.Webhook(ctx)
}

func (c *Controller) watchShutdown(
	ctx context.Context, runDone <-chan struct{}, cancelReconciliation context.CancelFunc, shutdownWait time.Duration,
) {
	go func() {
		select {
		case <-ctx.Done():
			if c.scheduler != nil {
				c.scheduler.Shutdown()
			}
			timer := time.NewTimer(shutdownWait)
			defer timer.Stop()
			select {
			case <-timer.C:
				cancelReconciliation()
			case <-runDone:
			}
		case <-runDone:
		}
	}()
}

func (c *Controller) acquireOwnership(ctx context.Context) (*ownershipHandle, error) {
	slog.InfoContext(ctx, "controller waiting for deployment ownership", slog.String("lock_file", c.ownership.path))
	handle, err := c.ownership.acquire(ctx)
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "controller acquired deployment ownership", slog.String("lock_file", c.ownership.path))
	return handle, nil
}

func (c *Controller) releaseOwnership(ctx context.Context, handle *ownershipHandle) {
	if err := handle.release(); err != nil {
		slog.ErrorContext(context.WithoutCancel(ctx), "release controller ownership", "err", err)
	}
}
