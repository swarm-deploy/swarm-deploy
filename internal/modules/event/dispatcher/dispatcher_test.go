package dispatcher

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestSubscribeMany(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := NewOutboxDispatcher(outbox.New(db))
	types := []events.TypeName{events.TypeNameNodeJoined, events.TypeNameDeployFailed}
	handler, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, SubscribeMany(bus, types, "multi", handler))
	for _, event := range []events.Event{&events.NodeJoined{}, &events.DeployFailed{}} {
		require.NoError(t, bus.Publish(t.Context(), event))
	}
	var deliveries int
	require.NoError(t, db.Get(t.Context(), &deliveries, "SELECT count(*) FROM outbox_deliveries WHERE subscription_id='multi'"))
	require.Equal(t, 2, deliveries)
	require.ErrorContains(t, SubscribeMany(bus, types, "multi", handler), "duplicate")
	require.ErrorContains(t, SubscribeMany(bus, []events.TypeName{"Unknown"}, "bad", handler), "unknown")
}
