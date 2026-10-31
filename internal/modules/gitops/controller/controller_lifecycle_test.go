package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/scheduler"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	"go.uber.org/mock/gomock"
)

func TestControllerGracefulShutdownPreservesActiveReconciliation(t *testing.T) {
	ctrl := gomock.NewController(t)
	runner := scheduler.NewMockRunner(ctrl)
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, task syncrun.Task) {
		assert.Equal(t, syncrun.TriggerStartup, task.Reason)
		started <- ctx
		<-release
	})

	controller := &Controller{
		scheduler: scheduler.New(controllerTestConfig(), runner),
		shutdownWait: 200 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()

	var reconciliationCtx context.Context
	select {
	case reconciliationCtx = <-started:
	case <-time.After(time.Second):
		cancel()
		require.FailNow(t, "startup reconciliation did not begin")
	}
	cancel()
	require.Eventually(t, func() bool { return !controller.Manual(t.Context()) }, time.Second, time.Millisecond,
		"shutdown must reject new work")
	assert.NoError(t, reconciliationCtx.Err(), "in-flight reconciliation should survive initial shutdown")
	select {
	case <-done:
		require.FailNow(t, "controller stopped before reconciliation completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-done)
}

func TestControllerCancelsReconciliationAfterGracePeriod(t *testing.T) {
	ctrl := gomock.NewController(t)
	runner := scheduler.NewMockRunner(ctrl)
	started := make(chan context.Context, 1)
	canceled := make(chan error, 1)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, _ syncrun.Task) {
		started <- ctx
		<-ctx.Done()
		canceled <- ctx.Err()
	})
	controller := &Controller{
		scheduler: scheduler.New(controllerTestConfig(), runner),
		shutdownWait: 100 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		require.FailNow(t, "startup reconciliation did not begin")
	}
	cancel()
	select {
	case <-canceled:
		require.FailNow(t, "reconciliation canceled before grace period")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case err := <-canceled:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		require.FailNow(t, "reconciliation did not cancel after grace period")
	}
	require.NoError(t, <-done)
}

func TestControllersSharingDataDirectoryDoNotReconcileConcurrently(t *testing.T) {
	ctrl := gomock.NewController(t)
	dataDir := t.TempDir()
	cfg := controllerTestConfig()
	cfg.Spec.DataDir = dataDir
	firstRunner := scheduler.NewMockRunner(ctrl)
	secondRunner := scheduler.NewMockRunner(ctrl)
	firstStarted, secondStarted := make(chan struct{}), make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)

	firstRunner.EXPECT().Run(gomock.Any(), gomock.Any()).Do(func(_ context.Context, task syncrun.Task) {
		assert.Equal(t, syncrun.TriggerStartup, task.Reason)
		close(firstStarted)
		<-releaseFirst
	})
	secondRunner.EXPECT().Run(gomock.Any(), gomock.Any()).Do(func(_ context.Context, task syncrun.Task) {
		assert.Equal(t, syncrun.TriggerStartup, task.Reason)
		close(secondStarted)
	})

	first := &Controller{
		scheduler: scheduler.New(cfg, firstRunner), ownership: newControllerOwnership(dataDir),
		shutdownWait: time.Second,
	}
	second := &Controller{
		scheduler: scheduler.New(cfg, secondRunner), ownership: newControllerOwnership(dataDir),
		shutdownWait: time.Second,
	}
	first.ownership.retryInterval = time.Millisecond
	second.ownership.retryInterval = time.Millisecond

	ctx1, cancel1 := context.WithCancel(t.Context())
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel1()
	defer cancel2()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- first.Run(ctx1) }()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "first controller did not start")
	}
	go func() { secondDone <- second.Run(ctx2) }()
	select {
	case <-secondStarted:
		require.FailNow(t, "second controller reconciled while first held ownership")
	case <-time.After(40 * time.Millisecond):
	}
	cancel1()
	release()
	require.NoError(t, <-firstDone)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "second controller did not acquire released ownership")
	}
	cancel2()
	require.NoError(t, <-secondDone)
}

func controllerTestConfig() *config.Config {
	return &config.Config{Spec: config.Spec{Sync: config.SyncSpec{
		Mode: config.SyncModeWebhook,
		Interval: specw.Duration{Value: time.Hour},
	}}}
}
