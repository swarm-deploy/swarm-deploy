package dispatcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"go.uber.org/mock/gomock"
)

func setup(t *testing.T) (*storage.Database, *OutboxDispatcher) {
	t.Helper()
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(t.Context(), `CREATE TABLE projection (source_event_id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	return db, NewOutboxDispatcher(outbox.New(db))
}

func rows(t *testing.T, db *storage.Database, table string) int {
	t.Helper()
	var count int
	require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM "+table))
	return count
}

func TestPublisherTransaction(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			db, bus := setup(t)
			handler := NewMockSubscriber(gomock.NewController(t))
			if !rollback {
				handler.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
			}
			require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
			sentinel := errors.New("publisher rollback")
			err := db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, err := db.Exec(ctx, "INSERT INTO projection VALUES ('business-state')")
				if err != nil {
					return err
				}
				require.NoError(t, bus.Publish(ctx, &events.NodeJoined{NodeID: "n1"}))
				assert.Zero(t, rows(t, db, "outbox_events"))
				assert.Zero(t, rows(t, db, "projection"))
				if rollback {
					return sentinel
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, sentinel)
			} else {
				require.NoError(t, err)
			}
			handled, err := bus.ProcessNext(t.Context())
			require.NoError(t, err)
			assert.Equal(t, !rollback, handled)
			assert.Zero(t, rows(t, db, "outbox_events"))
			assert.Zero(t, rows(t, db, "outbox_deliveries"))
			failed, err := bus.repository.ListFailed(t.Context())
			require.NoError(t, err)
			assert.Empty(t, failed)
		})
	}
}

func TestProjectionAndChildPublicationRollbackTogether(t *testing.T) {
	db, bus := setup(t)
	ctrl := gomock.NewController(t)
	parent, child := NewMockSubscriber(ctrl), NewMockSubscriber(ctrl)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "projection", parent))
	require.NoError(t, bus.Subscribe(events.TypeNameNetworkCreated, "child", child))
	for _, fail := range []bool{true, false} {
		parent.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, e events.Envelope) error {
			_, err := db.Exec(ctx, "INSERT INTO projection VALUES (?) ON CONFLICT DO NOTHING", e.ID)
			if err != nil {
				return err
			}
			require.NoError(t, bus.Publish(ctx, &events.NetworkCreated{NetworkID: "child"}))
			if fail {
				return errors.New("fail projection")
			}
			return nil
		})
	}
	child.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ events.Envelope) error {
		assert.Equal(t, 1, rows(t, db, "projection"))
		return nil
	})
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "parent"}))
	ok, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Zero(t, rows(t, db, "projection"))
	assert.Equal(t, 1, rows(t, db, "outbox_events"))
	_, err = db.Exec(t.Context(), "UPDATE outbox_deliveries SET available_at=0")
	require.NoError(t, err)
	ok, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, rows(t, db, "projection"))
	assert.Equal(t, 1, rows(t, db, "outbox_events"))
	ok, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Zero(t, rows(t, db, "outbox_events"))
}

func TestIndependentDestinationsAndReplay(t *testing.T) {
	for _, action := range []string{"replay", "discard"} {
		t.Run(action, func(t *testing.T) {
			db, bus := setup(t)
			bus.worker = outbox.NewWorker(bus.repository, bus.resolve, outbox.WithMaxAttempts(1))
			ctrl := gomock.NewController(t)
			good, bad := NewMockSubscriber(ctrl), NewMockSubscriber(ctrl)
			good.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ events.Envelope) error {
				// An external consumer can open an independent write transaction.
				return db.WithinTransaction(ctx, func(ctx context.Context) error {
					_, err := db.Exec(ctx, "INSERT INTO projection VALUES ('sent')")
					return err
				})
			})
			bad.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(errors.New("token=private-value"))
			require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "a-good", External(good)))
			require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "b-bad", External(bad)))
			require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
			for range 2 {
				_, err := bus.ProcessNext(t.Context())
				require.NoError(t, err)
			}
			assert.Equal(t, 1, rows(t, db, "outbox_events"))
			var failedRow struct {
				ID   string `db:"event_id"`
				Code string `db:"last_error"`
			}
			require.NoError(t, db.Get(t.Context(), &failedRow,
				"SELECT event_id,last_error FROM outbox_deliveries WHERE status='failed'"))
			assert.Equal(t, "handler_failed", failedRow.Code)
			failed, err := bus.repository.ListFailed(t.Context())
			require.NoError(t, err)
			require.Len(t, failed, 1)
			assert.Equal(t, outbox.FailedDelivery{EventID: failedRow.ID, SubscriptionID: "b-bad",
				EventType: string(events.TypeNameNodeJoined), Attempts: 1, LastError: "handler_failed"}, failed[0])
			if action == "replay" {
				bad.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
				require.NoError(t, bus.repository.Replay(t.Context(), failedRow.ID, "b-bad", bus.now()))
				_, err := bus.ProcessNext(t.Context())
				require.NoError(t, err)
			} else {
				require.NoError(t, bus.repository.Discard(t.Context(), failedRow.ID, "b-bad", bus.now()))
			}
			assert.Zero(t, rows(t, db, "outbox_events"))
			assert.Zero(t, rows(t, db, "outbox_deliveries"))
		})
	}
}

func TestLeaseRecoveryAndFencing(t *testing.T) {
	db, bus := setup(t)
	handler := NewMockSubscriber(gomock.NewController(t))
	handler.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
	first, err := bus.worker.Claim(t.Context())
	require.NoError(t, err)
	_, err = bus.worker.Claim(t.Context())
	require.Error(t, err)
	// A fresh worker simulates process restart; it cannot use an unexpired lease.
	restarted := NewOutboxDispatcher(outbox.New(db))
	require.NoError(t, restarted.Subscribe(events.TypeNameNodeJoined, "history", handler))
	_, err = db.Exec(t.Context(), "UPDATE outbox_deliveries SET lease_until=0")
	require.NoError(t, err)
	second, err := restarted.worker.Claim(t.Context())
	require.NoError(t, err)
	assert.NotEqual(t, first.Token, second.Token)
	require.ErrorIs(t, bus.worker.Process(t.Context(), first), outbox.ErrLeaseLost)
	require.ErrorIs(t, db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		return bus.worker.Acknowledge(ctx, first)
	}), outbox.ErrLeaseLost)
	require.NoError(t, restarted.worker.Process(t.Context(), second))
	assert.Zero(t, rows(t, db, "outbox_events"))
}

func TestConcurrentClaim(t *testing.T) {
	_, bus := setup(t)
	handler := NewMockSubscriber(gomock.NewController(t))
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := bus.worker.Claim(t.Context()); results <- err })
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	assert.Equal(t, 1, success)
}

func TestNoSubscribersAndNoGlobalDedup(t *testing.T) {
	db, bus := setup(t)
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
	assert.Zero(t, rows(t, db, "outbox_events"))
	handler := NewMockSubscriber(gomock.NewController(t))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", handler))
	for range 2 {
		require.NoError(t, bus.Publish(t.Context(), &events.DeploySuccess{}))
	}
	assert.Equal(t, 2, rows(t, db, "outbox_events"))
}

func TestInvalidDeliveryIsParked(t *testing.T) {
	for _, reason := range []string{"subscription_missing", "unsupported_or_invalid_payload"} {
		t.Run(reason, func(t *testing.T) {
			db, bus := setup(t)
			handler := NewMockSubscriber(gomock.NewController(t))
			require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
			require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
			if reason == "subscription_missing" {
				bus = NewOutboxDispatcher(outbox.New(db))
			} else {
				_, err := db.Exec(t.Context(), "UPDATE outbox_events SET schema_version=999")
				require.NoError(t, err)
			}
			handled, err := bus.ProcessNext(t.Context())
			require.NoError(t, err)
			require.True(t, handled)
			var row struct {
				Status string `db:"status"`
				Code   string `db:"last_error"`
			}
			require.NoError(t, db.Get(t.Context(), &row, "SELECT status,last_error FROM outbox_deliveries"))
			assert.Equal(t, "failed", row.Status)
			assert.Equal(t, reason, row.Code)
			assert.Equal(t, 1, rows(t, db, "outbox_events"))
		})
	}
}

func TestExpiredLeaseRollsBackProjection(t *testing.T) {
	db, bus := setup(t)
	now := time.Now()
	bus.now = func() time.Time { return now }
	bus.worker = outbox.NewWorker(bus.repository, bus.resolve, outbox.WithClock(bus.now))
	handler := NewMockSubscriber(gomock.NewController(t))
	handler.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, e events.Envelope) error {
		_, err := db.Exec(ctx, "INSERT INTO projection VALUES (?)", e.ID)
		now = now.Add(5*time.Minute + time.Second)
		return err
	})
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
	d, err := bus.worker.Claim(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, bus.worker.Process(t.Context(), d), outbox.ErrLeaseLost)
	assert.Zero(t, rows(t, db, "projection"))
	assert.Equal(t, 1, rows(t, db, "outbox_events"))
}

func TestWorkerDoesNotDeliverBeforeCommit(t *testing.T) {
	db, bus := setup(t)
	handler := NewMockSubscriber(gomock.NewController(t))
	delivered := make(chan struct{}, 1)
	handler.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, events.Envelope) error { delivered <- struct{}{}; return nil })
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	workerCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	workerDone := make(chan error, 1)
	err := db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		require.NoError(t, bus.Publish(ctx, &events.NodeJoined{}))
		go func() { workerDone <- bus.Run(workerCtx) }()
		select {
		case <-delivered:
			t.Error("handler called before commit")
		case <-time.After(50 * time.Millisecond):
		}
		return nil
	})
	require.NoError(t, err)
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("committed event was not delivered")
	}
	cancel()
	require.NoError(t, <-workerDone)
}

func TestWorkerPoolDoesNotLetExternalDeliveryBlockProjection(t *testing.T) {
	db, bus := setup(t)
	ctrl := gomock.NewController(t)
	external := NewMockSubscriber(ctrl)
	projection := NewMockSubscriber(ctrl)
	externalStarted := make(chan struct{})
	releaseExternal := make(chan struct{})
	projected := make(chan struct{})
	external.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, events.Envelope) error {
		close(externalStarted)
		<-releaseExternal
		return nil
	})
	projection.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ events.Envelope) error {
		close(projected)
		return nil
	})
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "a-external", External(external)))
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "z-projection", projection))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
	workerCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- bus.Run(workerCtx) }()
	select {
	case <-externalStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("external subscriber did not start")
	}
	select {
	case <-projected:
	case <-time.After(5 * time.Second):
		t.Fatal("database projection was blocked by external subscriber")
	}
	close(releaseExternal)
	require.Eventually(t, func() bool { return rows(t, db, "outbox_events") == 0 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestWorkerPoolPreservesSubscriptionOrder(t *testing.T) {
	_, bus := setup(t)
	handler := NewMockSubscriber(gomock.NewController(t))
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	gomock.InOrder(
		handler.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, events.Envelope) error {
			close(firstStarted)
			<-releaseFirst
			return nil
		}),
		handler.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, events.Envelope) error {
			close(secondStarted)
			return nil
		}),
	)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "ordered", External(handler)))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "first"}))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "second"}))
	workerCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- bus.Run(workerCtx) }()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first delivery did not start")
	}
	select {
	case <-secondStarted:
		t.Fatal("second delivery started before the first was acknowledged")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("second delivery did not start after the first was acknowledged")
	}
	cancel()
	require.NoError(t, <-done)
}

func TestLeaseSurvivesDatabaseRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(t.Context(), dir)
	require.NoError(t, err)
	bus := NewOutboxDispatcher(outbox.New(db))
	handler := NewMockSubscriber(gomock.NewController(t))
	handler.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "persisted"}))
	first, err := bus.worker.Claim(t.Context())
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = storage.Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus = NewOutboxDispatcher(outbox.New(db))
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	ok, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	assert.False(t, ok, "an unexpired lease must survive restart")
	bus.worker = outbox.NewWorker(bus.repository, bus.resolve,
		outbox.WithClock(func() time.Time { return time.Now().Add(5*time.Minute + time.Second) }))
	second, err := bus.worker.Claim(t.Context())
	require.NoError(t, err)
	assert.Equal(t, first.EventID, second.EventID)
	assert.Equal(t, 2, second.Attempts)
	require.NoError(t, bus.worker.Process(t.Context(), second))
	assert.Zero(t, rows(t, db, "outbox_events"))
}

func TestEnqueueFailureRollsBackWholePublication(t *testing.T) {
	db, bus := setup(t)
	handler := NewMockSubscriber(gomock.NewController(t))
	for _, id := range []string{"good", "bad"} {
		require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, id, handler))
	}
	_, err := db.Exec(t.Context(), `CREATE TRIGGER reject_delivery
		BEFORE INSERT ON outbox_deliveries WHEN NEW.subscription_id='bad'
		BEGIN SELECT RAISE(ABORT, 'delivery rejected'); END`)
	require.NoError(t, err)
	require.Error(t, bus.Publish(t.Context(), &events.NodeJoined{}))
	assert.Zero(t, rows(t, db, "outbox_events"))
	assert.Zero(t, rows(t, db, "outbox_deliveries"))
}

func TestRegistrationValidation(t *testing.T) {
	_, bus := setup(t)
	handler := NewMockSubscriber(gomock.NewController(t))
	for _, typ := range events.Types {
		require.NoError(t, bus.Subscribe(typ.Name(), "subscriber", handler))
		require.ErrorContains(t, bus.Subscribe(typ.Name(), "subscriber", handler), "duplicate")
	}
	require.ErrorContains(t, bus.Subscribe("Unknown", "subscriber", handler), "unknown event type")
	require.Error(t, bus.Subscribe(events.TypeNameNodeJoined, "", handler))
	require.Error(t, bus.Subscribe(events.TypeNameNodeJoined, "nil", nil))
	_, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.ErrorContains(t, bus.Subscribe(events.TypeNameNodeJoined, "late", handler), "before starting")
}
