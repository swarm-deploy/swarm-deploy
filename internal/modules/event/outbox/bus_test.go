package outbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"go.uber.org/mock/gomock"
)

func setup(t *testing.T) (*storage.Database, *Bus) {
	t.Helper()
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(t.Context(), `CREATE TABLE projection (source_event_id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	return db, New(db)
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
			failed, err := bus.ListFailed(t.Context())
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
			bus.maxAttempts = 1
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
			failed, err := bus.ListFailed(t.Context())
			require.NoError(t, err)
			require.Len(t, failed, 1)
			assert.Equal(t, FailedDelivery{EventID: failedRow.ID, SubscriptionID: "b-bad",
				EventType: string(events.TypeNameNodeJoined), Attempts: 1, LastError: "handler_failed"}, failed[0])
			if action == "replay" {
				bad.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
				require.NoError(t, bus.Replay(t.Context(), failedRow.ID, "b-bad"))
				_, err := bus.ProcessNext(t.Context())
				require.NoError(t, err)
			} else {
				require.NoError(t, bus.Discard(t.Context(), failedRow.ID, "b-bad"))
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
	first, err := bus.claim(t.Context())
	require.NoError(t, err)
	_, err = bus.claim(t.Context())
	require.Error(t, err)
	// A fresh worker simulates process restart; it cannot use an unexpired lease.
	restarted := New(db)
	require.NoError(t, restarted.Subscribe(events.TypeNameNodeJoined, "history", handler))
	_, err = db.Exec(t.Context(), "UPDATE outbox_deliveries SET lease_until=0")
	require.NoError(t, err)
	second, err := restarted.claim(t.Context())
	require.NoError(t, err)
	assert.NotEqual(t, first.token, second.token)
	require.ErrorIs(t, bus.process(t.Context(), first), ErrLeaseLost)
	require.ErrorIs(t, db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		return bus.ack(ctx, first, false)
	}), ErrLeaseLost)
	require.NoError(t, restarted.process(t.Context(), second))
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
		wg.Go(func() { _, err := bus.claim(t.Context()); results <- err })
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

func TestClaimQueryUsesActiveSubscriptionIndex(t *testing.T) {
	testCases := []struct {
		name        string
		statuses    map[string]int
		subscribers int
	}{
		{name: "many delivered events", statuses: map[string]int{"delivered": 500, "pending": 5}, subscribers: 3},
		{name: "many pending deliveries", statuses: map[string]int{"pending": 500}, subscribers: 3},
		{name: "multiple subscribers", statuses: map[string]int{"pending": 100}, subscribers: 10},
		{name: "processing deliveries", statuses: map[string]int{"pending": 100, "processing": 100}, subscribers: 4},
		{name: "accumulated failed deliveries", statuses: map[string]int{"failed": 500, "pending": 5}, subscribers: 3},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db, _ := setup(t)
			sequence := 0
			require.NoError(t, db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				for status, count := range testCase.statuses {
					for range count {
						sequence++
						eventID := fmt.Sprintf("event-%06d", sequence)
						_, err := db.Exec(ctx, `INSERT INTO outbox_events
							(id,event_type,schema_version,occurred_at,payload) VALUES (?,?,?,?,?)`,
							eventID, events.TypeNameNodeJoined, 1, sequence, `{}`)
						if err != nil {
							return err
						}
						for subscriber := range testCase.subscribers {
							leaseUntil, leaseToken := any(nil), any(nil)
							if status == "processing" {
								leaseUntil, leaseToken = int64(sequence), "lease-token"
							}
							_, err = db.Exec(ctx, `INSERT INTO outbox_deliveries
								(event_id,subscription_id,status,available_at,lease_until,lease_token,updated_at)
								VALUES (?,?,?,?,?,?,?)`, eventID, fmt.Sprintf("subscriber-%02d", subscriber), status,
								sequence, leaseUntil, leaseToken, sequence)
							if err != nil {
								return err
							}
						}
					}
				}
				return nil
			}))
			_, err := db.Exec(t.Context(), "ANALYZE")
			require.NoError(t, err)
			now := time.Unix(0, 1_000_000).UTC()
			query, args, err := claimStatement(now, now, "token", time.Unix(0, 2_000_000).UTC(), now)
			require.NoError(t, err)
			var rows []struct {
				ID     int    `db:"id"`
				Parent int    `db:"parent"`
				Unused int    `db:"notused"`
				Detail string `db:"detail"`
			}
			require.NoError(t, db.Select(t.Context(), &rows, "EXPLAIN QUERY PLAN "+query, args...))
			var plan string
			for _, row := range rows {
				plan += row.Detail + "\n"
			}
			t.Log(plan)
			assert.Contains(t, plan, "idx_outbox_active_subscription")
		})
	}
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
				bus = New(db)
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
	handler := NewMockSubscriber(gomock.NewController(t))
	handler.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, e events.Envelope) error {
		_, err := db.Exec(ctx, "INSERT INTO projection VALUES (?)", e.ID)
		now = now.Add(bus.lease + time.Second)
		return err
	})
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))
	d, err := bus.claim(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, bus.process(t.Context(), d), ErrLeaseLost)
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
	bus := New(db)
	handler := NewMockSubscriber(gomock.NewController(t))
	handler.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "persisted"}))
	first, err := bus.claim(t.Context())
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = storage.Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus = New(db)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
	ok, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	assert.False(t, ok, "an unexpired lease must survive restart")
	bus.now = func() time.Time { return time.Now().Add(defaultLease + time.Second) }
	second, err := bus.claim(t.Context())
	require.NoError(t, err)
	assert.Equal(t, first.eventID, second.eventID)
	assert.Equal(t, 2, second.attempts)
	require.NoError(t, bus.process(t.Context(), second))
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

func TestNotificationDeliveryFailureSignal(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		maxAttempts int
		wantSignal  bool
	}{
		{name: "temporary failure followed by success", maxAttempts: 2},
		{name: "terminal failure", maxAttempts: 1, wantSignal: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, bus := setup(t)
			bus.maxAttempts = testCase.maxAttempts
			controller := gomock.NewController(t)
			notification := NewMockSubscriber(controller)
			notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(assert.AnError)
			if !testCase.wantSignal {
				notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
			}
			signal := NewMockSubscriber(controller)
			if testCase.wantSignal {
				signal.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, envelope events.Envelope) error {
						failure := envelope.Event.(*events.SendNotificationFailed)
						assert.Equal(t, events.TypeNameNodeJoined, failure.EventType.Name())
						assert.Equal(t, "telegram", failure.Destination)
						assert.Equal(t, "ops", failure.Channel)
						assert.EqualError(t, failure.Error, "handler_failed")
						return nil
					},
				)
			}
			require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "notification",
				ExternalNotification(notification, "telegram", "ops", "telegram:test")))
			require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", signal))
			require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

			handled, err := bus.ProcessNext(t.Context())
			require.NoError(t, err)
			require.True(t, handled)
			var signals int
			require.NoError(t, db.Get(t.Context(), &signals,
				"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameSendNotificationFailed))
			assert.Equal(t, map[bool]int{false: 0, true: 1}[testCase.wantSignal], signals)

			if !testCase.wantSignal {
				_, err = db.Exec(t.Context(), "UPDATE outbox_deliveries SET available_at=0 WHERE status='pending'")
				require.NoError(t, err)
				handled, err = bus.ProcessNext(t.Context())
				require.NoError(t, err)
				require.True(t, handled)
				assert.Zero(t, rows(t, db, "outbox_events"))
				return
			}

			restarted := New(db)
			restarted.maxAttempts = testCase.maxAttempts
			require.NoError(t, restarted.Subscribe(events.TypeNameNodeJoined, "notification",
				ExternalNotification(notification, "telegram", "ops", "telegram:test")))
			require.NoError(t, restarted.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", signal))
			handled, err = restarted.ProcessNext(t.Context())
			require.NoError(t, err)
			require.True(t, handled)
			handled, err = restarted.ProcessNext(t.Context())
			require.NoError(t, err)
			assert.False(t, handled, "restart must not republish a terminal transition")
		})
	}
}

func TestNotificationDestinationIdentityIsNonReversible(t *testing.T) {
	const endpoint = "https://user:secret@example.com/hook?token=private"
	key := NotificationDestinationKey("custom", endpoint)
	assert.True(t, strings.HasPrefix(key, "custom:"))
	assert.NotContains(t, key, endpoint)
	assert.NotContains(t, key, "secret")
	assert.Equal(t, key, NotificationDestinationKey("custom", endpoint))
}

func TestMissingNotificationSubscriptionDoesNotPublishDeliveryFailure(t *testing.T) {
	db, bus := setup(t)
	notification := NewMockSubscriber(gomock.NewController(t))
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "removed-route",
		ExternalNotification(notification, "telegram", "infrastructure", "telegram:physical")))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	restarted := New(db)
	signal := NewMockSubscriber(gomock.NewController(t))
	require.NoError(t, restarted.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", signal))
	handled, err := restarted.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	handled, err = restarted.ProcessNext(t.Context())
	require.NoError(t, err)
	assert.False(t, handled)
	var failureEvents int
	require.NoError(t, db.Get(t.Context(), &failureEvents,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameSendNotificationFailed))
	assert.Zero(t, failureEvents)
	var delivery struct {
		Status string `db:"status"`
		Code   string `db:"last_error"`
	}
	require.NoError(t, db.Get(t.Context(), &delivery,
		"SELECT status,last_error FROM outbox_deliveries"))
	assert.Equal(t, "failed", delivery.Status)
	assert.Equal(t, "subscription_missing", delivery.Code)
}

func TestLegacyCustomWebhookUsesRegisteredPhysicalDestination(t *testing.T) {
	db, bus := setup(t)
	bus.maxAttempts = 1
	const endpoint = "https://example.test/hooks/notify"
	physicalKey := NotificationDestinationKey("custom", endpoint)
	historicalIdentity := fmt.Sprintf("%x", sha256.Sum256([]byte("POST "+endpoint)))
	subscriptionID := "notification:custom:ops:" + historicalIdentity
	notification := NewMockSubscriber(gomock.NewController(t))
	notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(assert.AnError)
	signal := NewMockSubscriber(gomock.NewController(t))
	signal.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, envelope events.Envelope) error {
			failure := envelope.Event.(*events.SendNotificationFailed)
			assert.Equal(t, physicalKey, failure.DestinationKey)
			assert.NotEqual(t, "custom:"+historicalIdentity, failure.DestinationKey)
			return nil
		},
	)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, subscriptionID,
		ExternalNotification(notification, "custom", "ops", physicalKey)))
	require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", signal))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))
	_, err := db.Exec(t.Context(), `UPDATE outbox_deliveries SET
		notification_destination=NULL,notification_channel=NULL,notification_destination_key=NULL
		WHERE subscription_id=?`, subscriptionID)
	require.NoError(t, err)

	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	handled, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
}

func TestActiveNotificationMetadataTakesPrecedenceAfterRestart(t *testing.T) {
	db, bus := setup(t)
	const subscriptionID = "notification:custom:ops:stable-route"
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, subscriptionID,
		ExternalNotification(NewMockSubscriber(gomock.NewController(t)), "custom", "original", "custom:original")))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	restarted := New(db)
	restarted.maxAttempts = 1
	notification := NewMockSubscriber(gomock.NewController(t))
	notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(assert.AnError)
	signal := NewMockSubscriber(gomock.NewController(t))
	signal.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, envelope events.Envelope) error {
			failure := envelope.Event.(*events.SendNotificationFailed)
			assert.Equal(t, "changed", failure.Channel)
			assert.Equal(t, "custom:changed", failure.DestinationKey)
			return nil
		},
	)
	require.NoError(t, restarted.Subscribe(events.TypeNameNodeJoined, subscriptionID,
		ExternalNotification(notification, "custom", "changed", "custom:changed")))
	require.NoError(t, restarted.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", signal))

	handled, err := restarted.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	handled, err = restarted.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
}

// Pending work may be delivered through a newly configured webhook handler.
// Its successful outcome must heal the active destination, not the old one.
func TestActiveNotificationMetadataUsedForRecoveredSuccess(t *testing.T) {
	db, bus := setup(t)
	const subscriptionID = "notification:custom:ops:stable-route"
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, subscriptionID,
		ExternalNotification(NewMockSubscriber(gomock.NewController(t)), "custom", "original", "custom:original")))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	restarted := New(db)
	notification := NewMockSubscriber(gomock.NewController(t))
	notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
	signal := NewMockSubscriber(gomock.NewController(t))
	signal.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, envelope events.Envelope) error {
			sent := envelope.Event.(*events.NotificationSent)
			assert.Equal(t, "changed", sent.Channel)
			assert.Equal(t, "custom:changed", sent.DestinationKey)
			return nil
		},
	)
	require.NoError(t, restarted.Subscribe(events.TypeNameNodeJoined, subscriptionID,
		ExternalNotification(notification, "custom", "changed", "custom:changed")))
	require.NoError(t, restarted.Subscribe(events.TypeNameNotificationSent, "alert-management", signal))

	for range 2 {
		handled, err := restarted.ProcessNext(t.Context())
		require.NoError(t, err)
		require.True(t, handled)
	}
}

func TestTerminalNotificationFailurePublicationIsAtomic(t *testing.T) {
	db, bus := setup(t)
	bus.maxAttempts = 1
	notification := NewMockSubscriber(gomock.NewController(t))
	notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(assert.AnError)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "notification",
		ExternalNotification(notification, "custom", "audit", "custom:test")))
	require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", notification))
	_, err := db.Exec(t.Context(), `CREATE TRIGGER reject_failure_signal
		BEFORE INSERT ON outbox_events WHEN NEW.event_type='SendNotificationFailed'
		BEGIN SELECT RAISE(ABORT, 'failure signal rejected'); END`)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))

	handled, err := bus.ProcessNext(t.Context())
	require.ErrorContains(t, err, "failure signal rejected")
	require.True(t, handled)
	var status string
	require.NoError(t, db.Get(t.Context(), &status, "SELECT status FROM outbox_deliveries"))
	assert.Equal(t, "processing", status, "terminal transition must roll back with its event")
}

func TestNotificationSentAfterSuccessfulDelivery(t *testing.T) {
	db, bus := setup(t)
	controller := gomock.NewController(t)
	notification := NewMockSubscriber(controller)
	notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
	signal := NewMockSubscriber(controller)
	signal.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, envelope events.Envelope) error {
			sent := envelope.Event.(*events.NotificationSent)
			assert.Equal(t, events.TypeNameNodeJoined, sent.EventType.Name())
			assert.Equal(t, "telegram", sent.Destination)
			assert.Equal(t, "ops", sent.Channel)
			assert.Equal(t, "telegram:test", sent.DestinationKey)
			return nil
		},
	)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "notification",
		ExternalNotification(notification, "telegram", "ops", "telegram:test")))
	require.NoError(t, bus.Subscribe(events.TypeNameNotificationSent, "alert-management", signal))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	var successEvents int
	require.NoError(t, db.Get(t.Context(), &successEvents,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameNotificationSent))
	assert.Equal(t, 1, successEvents)
	handled, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	assert.Zero(t, rows(t, db, "outbox_events"))
}

type noOpNotificationHandler struct{}

func (noOpNotificationHandler) Handle(context.Context, events.Envelope) error { return nil }

func (noOpNotificationHandler) HandleNotification(context.Context, events.Envelope) (bool, error) {
	return false, nil
}

func TestNoOpNotificationDoesNotPublishSuccess(t *testing.T) {
	db, bus := setup(t)
	signal := NewMockSubscriber(gomock.NewController(t))
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "notification",
		ExternalNotification(noOpNotificationHandler{}, "telegram", "ops", "telegram:test")))
	require.NoError(t, bus.Subscribe(events.TypeNameNotificationSent, "alert-management", signal))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	assert.Zero(t, rows(t, db, "outbox_events"))
}

func TestNotificationSuccessPublicationIsAtomic(t *testing.T) {
	db, bus := setup(t)
	notification := NewMockSubscriber(gomock.NewController(t))
	notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "notification",
		ExternalNotification(notification, "custom", "audit", "custom:test")))
	require.NoError(t, bus.Subscribe(events.TypeNameNotificationSent, "alert-management", notification))
	_, err := db.Exec(t.Context(), `CREATE TRIGGER reject_success_signal
		BEFORE INSERT ON outbox_events WHEN NEW.event_type='NotificationSent'
		BEGIN SELECT RAISE(ABORT, 'success signal rejected'); END`)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{}))

	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	var status string
	require.NoError(t, db.Get(t.Context(), &status, "SELECT status FROM outbox_deliveries"))
	assert.Equal(t, "pending", status, "successful transition must roll back with its event")
	var successEvents int
	require.NoError(t, db.Get(t.Context(), &successEvents,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameNotificationSent))
	assert.Zero(t, successEvents)
}

func TestSendNotificationFailedDeliveryDoesNotRecurse(t *testing.T) {
	for _, event := range []events.Event{
		&events.SendNotificationFailed{
			EventType: events.TypeNodeJoined, Destination: "custom", Channel: "audit", Error: assert.AnError,
		},
		&events.NotificationSent{
			EventType: events.TypeNodeJoined, Destination: "custom", Channel: "audit",
		},
	} {
		t.Run(event.Type().String(), func(t *testing.T) {
			db, bus := setup(t)
			bus.maxAttempts = 1
			notification := NewMockSubscriber(gomock.NewController(t))
			notification.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(assert.AnError)
			require.NoError(t, bus.Subscribe(event.Type().Name(), "notification",
				ExternalNotification(notification, "telegram", "ops", "telegram:test")))
			require.NoError(t, bus.Publish(t.Context(), event))

			handled, err := bus.ProcessNext(t.Context())
			require.NoError(t, err)
			require.True(t, handled)
			assert.Equal(t, 1, rows(t, db, "outbox_events"))
			assert.Equal(t, 1, rows(t, db, "outbox_deliveries"))
		})
	}
}

// MockSubscriber acts as an actual notification sender in Outbox delivery tests.
func (m *MockSubscriber) HandleNotification(ctx context.Context, event events.Envelope) (bool, error) {
	err := m.Handle(ctx, event)
	return err == nil, err
}
