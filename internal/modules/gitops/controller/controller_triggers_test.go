package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/scheduler"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"go.uber.org/mock/gomock"
)

func TestControllerManualAndWebhookTriggers(t *testing.T) {
	ctrl := gomock.NewController(t)
	runner := scheduler.NewMockRunner(ctrl)
	startup := make(chan struct{})
	release := make(chan struct{})
	tasks := make(chan syncrun.Task, 2)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Do(func(_ context.Context, task syncrun.Task) {
		if task.Reason == syncrun.TriggerStartup {
			close(startup)
			<-release
			return
		}
		tasks <- task
	}).AnyTimes()

	bus := dispatcher.NewMockDispatcher(ctrl)
	var receipts []bool
	bus.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event events.Event) error {
		receipt, ok := event.(*events.WebhookReceived)
		require.True(t, ok)
		receipts = append(receipts, receipt.Queued)
		return nil
	}).Times(2)

	cfg := &config.Config{Spec: config.Spec{Sync: config.SyncSpec{
		Mode: config.SyncModeWebhook, Interval: specw.Duration{Value: time.Hour},
	}}}
	queue := scheduler.New(cfg, runner)
	controller := &Controller{scheduler: queue, event: bus}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- queue.Run(ctx, context.WithoutCancel(ctx)) }()
	t.Cleanup(func() {
		cancel()
		queue.Shutdown()
		select {
		case <-release:
		default:
			close(release)
		}
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Error("scheduler did not exit")
		}
	})

	select {
	case <-startup:
	case <-time.After(time.Second):
		require.FailNow(t, "startup sync did not begin")
	}

	userCtx := security.ContextWithUser(t.Context(), security.User{Name: "alice"})
	require.True(t, controller.Manual(userCtx))
	queued, err := controller.Webhook(t.Context())
	require.NoError(t, err)
	assert.False(t, queued, "webhook must be rejected when manual sync occupies the single queue slot")
	close(release)

	select {
	case task := <-tasks:
		assert.Equal(t, syncrun.TriggerManual, task.Reason)
		assert.Equal(t, "alice", task.TriggeredBy)
	case <-time.After(time.Second):
		require.FailNow(t, "manual sync was not executed")
	}

	queued, err = controller.Webhook(t.Context())
	require.NoError(t, err)
	require.True(t, queued)
	select {
	case task := <-tasks:
		assert.Equal(t, syncrun.TriggerWebhook, task.Reason)
	case <-time.After(time.Second):
		require.FailNow(t, "webhook sync was not executed")
	}
	assert.Equal(t, []bool{false, true}, receipts)
}

func TestControllerWebhookPublishesReceiptWhenSchedulerIsInactive(t *testing.T) {
	ctrl := gomock.NewController(t)
	publishErr := errors.New("outbox unavailable")
	bus := dispatcher.NewMockDispatcher(ctrl)
	bus.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event events.Event) error {
			receipt, ok := event.(*events.WebhookReceived)
			require.True(t, ok)
			assert.False(t, receipt.Queued)
			return publishErr
		},
	)
	controller := &Controller{
		scheduler: scheduler.New(&config.Config{}, scheduler.NewMockRunner(ctrl)),
		event:     bus,
	}
	queued, err := controller.Webhook(t.Context())
	assert.False(t, queued)
	require.ErrorIs(t, err, publishErr)
}
