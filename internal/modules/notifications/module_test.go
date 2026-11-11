package notifications

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/eventnotify"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestInitModuleDeliversPendingWebhookAfterRestart(t *testing.T) {
	ctx := t.Context()
	received := make(chan eventnotify.NodePayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct{ Payload eventnotify.NodePayload }
		assert.Equal(t, http.MethodPost, r.Method)
		if assert.NoError(t, json.NewDecoder(r.Body).Decode(&message)) {
			received <- message.Payload
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	var endpoint specw.URL
	require.NoError(t, endpoint.UnmarshalString(server.URL))
	spec := config.NotificationSpec{
		Events: map[events.TypeName]config.NotificationEventRoutes{
			events.TypeNameNodeJoined: {
				Custom: []config.CustomChannel{{
					Name: "hooks", Method: http.MethodPost, URL: specw.Env[specw.URL]{Value: endpoint},
				}},
			},
		},
	}

	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	// This is the subscription ID used before Notifications had its own initializer.
	subscriptionID := fmt.Sprintf("notification:custom:hooks:%x", sha256.Sum256([]byte("POST "+server.URL)))
	err = outbox.New(db).Publish(ctx, &events.NodeJoined{NodeID: "node-1"}, []string{subscriptionID}, time.Now())
	require.NoError(t, db.Close())
	require.NoError(t, err)

	db, err = storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repository := outbox.New(db)
	bus := dispatcher.NewOutboxDispatcher(repository)
	require.NoError(t, InitModule(ctx, spec, bus, db))

	handled, err := bus.ProcessNext(ctx)
	require.NoError(t, err)
	require.True(t, handled)
	select {
	case payload := <-received:
		assert.Equal(t, "node-1", payload.NodeID)
	default:
		t.Fatal("pending webhook delivery was not sent")
	}
	deliveries, err := repository.ListDeliveries(ctx, outbox.ListDeliveriesFilter{})
	require.NoError(t, err)
	assert.Empty(t, deliveries)
}
