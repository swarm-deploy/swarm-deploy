package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	"go.uber.org/mock/gomock"
)

func TestSchedulePreservesQueueFullAndShutdownBehavior(t *testing.T) {
	for _, test := range []struct {
		name, state string
		wantQueued  bool
	}{
		{name: "queued", wantQueued: true},
		{name: "queue full", state: "full"},
		{name: "shutting down", state: "shutdown"},
		{name: "inactive", state: "inactive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			scheduler := New(testConfig(), NewMockRunner(ctrl))
			if test.state != "inactive" {
				scheduler.active.Store(true)
			}
			if test.state == "full" {
				scheduler.queue <- syncrun.Task{Reason: syncrun.TriggerInterval}
			}
			if test.state == "shutdown" {
				scheduler.Shutdown()
			}
			queued := scheduler.Schedule(t.Context(), syncrun.Task{Reason: syncrun.TriggerWebhook})
			assert.Equal(t, test.wantQueued, queued)
			if queued {
				assert.Equal(t, syncrun.TriggerWebhook, (<-scheduler.queue).Reason)
			}
		})
	}
}

func TestSchedulePreservesTaskMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	scheduler := New(testConfig(), NewMockRunner(ctrl))
	scheduler.active.Store(true)
	require.True(t, scheduler.Schedule(t.Context(), syncrun.Task{
		TriggeredBy: "alice", Reason: syncrun.TriggerManual,
	}))
	task := <-scheduler.queue
	assert.Equal(t, syncrun.TriggerManual, task.Reason)
	assert.Equal(t, "alice", task.TriggeredBy)
}

func TestRunSchedulesPeriodicPollReconciliationAndCleanup(t *testing.T) {
	ctrl := gomock.NewController(t)
	runner := NewMockRunner(ctrl)
	pollSeen, intervalSeen, cleanupSeen := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var pollOnce, intervalOnce, cleanupOnce sync.Once
	runner.EXPECT().Poll(gomock.Any()).Do(func(context.Context) { pollOnce.Do(func() { close(pollSeen) }) }).AnyTimes()
	runner.EXPECT().Cleanup(gomock.Any()).Do(func(context.Context) { cleanupOnce.Do(func() { close(cleanupSeen) }) }).AnyTimes()
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Do(
		func(_ context.Context, task syncrun.Task) {
			if task.Reason == syncrun.TriggerInterval {
				intervalOnce.Do(func() { close(intervalSeen) })
			}
		},
	).AnyTimes()

	cfg := testConfig()
	cfg.Spec.Sync.Mode = config.SyncModeHybrid
	cfg.Spec.Sync.Interval.Value = time.Millisecond
	cfg.Spec.Sync.PollInterval.Value = time.Millisecond
	cfg.Spec.SecretRotation.Cleanup.Enabled = true
	cfg.Spec.SecretRotation.Cleanup.Interval.Value = time.Millisecond
	scheduler := New(cfg, runner)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx, context.WithoutCancel(ctx)) }()
	for _, seen := range []<-chan struct{}{pollSeen, intervalSeen, cleanupSeen} {
		select {
		case <-seen:
		case <-time.After(time.Second):
			cancel()
			require.FailNow(t, "periodic scheduler trigger was not observed")
		}
	}
	cancel()
	require.NoError(t, <-done)
}

func TestRunTriggersStartupAndWaitsForActiveReconciliation(t *testing.T) {
	ctrl := gomock.NewController(t)
	runner := NewMockRunner(ctrl)
	started := make(chan struct{})
	release := make(chan struct{})
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Do(func(_ context.Context, task syncrun.Task) {
		assert.Equal(t, syncrun.TriggerStartup, task.Reason)
		close(started)
		<-release
	})

	scheduler := New(testConfig(), runner)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx, context.WithoutCancel(ctx)) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		require.FailNow(t, "startup reconciliation was not triggered")
	}
	cancel()
	select {
	case <-done:
		require.FailNow(t, "scheduler returned before active reconciliation completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-done)
	assert.False(t, scheduler.Schedule(t.Context(), syncrun.Task{Reason: syncrun.TriggerManual}), "shutdown must reject new tasks")
}

func testConfig() *config.Config {
	return &config.Config{Spec: config.Spec{Sync: config.SyncSpec{
		Mode:         config.SyncModeWebhook,
		Interval:     specw.Duration{Value: time.Hour},
		PollInterval: specw.Duration{Value: time.Hour},
	}}}
}
