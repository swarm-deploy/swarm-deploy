package outbox

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

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
			db, err := storage.Open(t.Context(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			sequence := 0
			require.NoError(t, db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				for status, count := range testCase.statuses {
					for range count {
						sequence++
						eventID := fmt.Sprintf("event-%06d", sequence)
						_, insertErr := db.Exec(ctx, `INSERT INTO outbox_events
							(id,event_type,schema_version,occurred_at,payload) VALUES (?,?,?,?,?)`,
							eventID, events.TypeNameNodeJoined, 1, sequence, `{}`)
						if insertErr != nil {
							return insertErr
						}
						for subscriber := range testCase.subscribers {
							leaseUntil, leaseToken := any(nil), any(nil)
							if status == "processing" {
								leaseUntil, leaseToken = int64(sequence), "lease-token"
							}
							_, insertErr = db.Exec(ctx, `INSERT INTO outbox_deliveries
								(event_id,subscription_id,status,available_at,lease_until,lease_token,updated_at)
								VALUES (?,?,?,?,?,?,?)`, eventID, fmt.Sprintf("subscriber-%02d", subscriber), status,
								sequence, leaseUntil, leaseToken, sequence)
							if insertErr != nil {
								return insertErr
							}
						}
					}
				}
				return nil
			}))
			_, err = db.Exec(t.Context(), "ANALYZE")
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
