package alerts

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func alertSpec() config.NotificationSpec {
	return config.NotificationSpec{
		Messengers: config.NotificationMessengersSpec{Telegram: config.NotificationTelegramSpec{
			BotToken: specw.File{Content: []byte("123:token")}, Retries: 1,
			Chats: map[string]config.NotificationChatSpec{"default": {ID: "-100", ThreadID: 1}, "infra": {ID: "-101", ThreadID: 2}},
		}},
		Alerts: map[string]config.NotificationAlertRoutes{"node_disconnected": {Telegram: []config.TelegramAlertChannel{{
			Chats:    config.NotificationChatNames{"default", "infra"},
			Opened:   config.AlertMessageSpec{Message: "{{ .Title }}"},
			Resolved: config.AlertMessageSpec{Action: "reply", Message: "{{ .Resolution }}"},
		}}}},
	}
}

func subscribeTestRecipients(t *testing.T, bus *outbox.Bus, db *storage.Database, spec config.NotificationSpec, url string) []events.AlertRecipient {
	t.Helper()
	recipients := spec.AlertRecipients("node_disconnected")
	for _, recipient := range recipients {
		subscriber := outbox.External(&Subscriber{store: NewStore(db), spec: spec, recipient: recipient, now: time.Now, apiBaseURL: url})
		require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, SubscriptionID(recipient), subscriber))
		require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, SubscriptionID(recipient), subscriber))
	}
	return recipients
}

func drain(t *testing.T, bus *outbox.Bus) {
	t.Helper()
	for range 12 {
		handled, err := bus.ProcessNext(t.Context())
		require.NoError(t, err)
		if !handled {
			return
		}
	}
}

func TestRegisterIncludesFallbackAndCustomRecipientsForFutureAlertKinds(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	spec := alertSpec()
	spec.Messengers.Telegram.Chats["deployments"] = config.NotificationChatSpec{ID: "-102", ThreadID: 3}
	spec.Alerts = map[string]config.NotificationAlertRoutes{
		"default": {Telegram: []config.TelegramAlertChannel{{Chats: config.NotificationChatNames{"default"}}}},
		"deploy_failed": {Telegram: []config.TelegramAlertChannel{{
			Chats: config.NotificationChatNames{"deployments"},
		}}},
		"node_disconnected": {Telegram: []config.TelegramAlertChannel{{
			Chats: config.NotificationChatNames{"infra"},
		}}},
	}
	bus := outbox.New(db)
	require.NoError(t, Register(t.Context(), bus, db, spec))
	futureRecipients := spec.AlertRecipients("future_alert_kind")
	require.Len(t, futureRecipients, 1)
	require.NoError(t, bus.Publish(t.Context(), &events.AlertOpened{
		Alert: events.AlertSnapshot{
			ID: "future-alert", Kind: "future_alert_kind", Title: "Future alert", OpenedAt: time.Now().UTC(),
		},
		Recipients: futureRecipients,
	}))

	for _, kind := range []string{"future_alert_kind", "deploy_failed", "node_disconnected"} {
		recipients := spec.AlertRecipients(kind)
		require.Len(t, recipients, 1)
		var deliveries int
		require.NoError(t, db.Get(t.Context(), &deliveries, `SELECT count(*) FROM outbox_deliveries d
			JOIN outbox_events e ON e.id=d.event_id WHERE e.event_type='alertOpened' AND d.subscription_id=?`,
			SubscriptionID(recipients[0])))
		assert.Equal(t, 1, deliveries, "recipient for %s must be registered", kind)
	}
}

func TestAlertIndependentDeliveryOrderingAndRestart(t *testing.T) {
	directory := t.TempDir()
	db, err := storage.Open(t.Context(), directory)
	require.NoError(t, err)
	spec := alertSpec()
	var failInfra atomic.Bool
	failInfra.Store(true)
	counts := map[string]int{}
	var infraReplies []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
			Reply  *struct {
				MessageID int64 `json:"message_id"`
			} `json:"reply_parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode Telegram request: %v", err)
			return
		}
		if body.ChatID == "-101" && failInfra.Load() {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"ok":false,"description":"unavailable"}`))
			return
		}
		counts[body.ChatID]++
		if body.ChatID == "-101" && body.Reply != nil {
			infraReplies = append(infraReplies, body.Reply.MessageID)
		}
		id := 100 + counts[body.ChatID]
		if body.ChatID == "-101" {
			id += 100
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":` + strconv.Itoa(id) + `}}`))
	}))
	defer server.Close()
	bus := outbox.New(db)
	recipients := subscribeTestRecipients(t, bus, db, spec, server.URL)
	alert := events.AlertSnapshot{ID: "cycle-1", Kind: "node_disconnected", Title: "Node disconnected", ResourceID: "node-1", OpenedAt: time.Now().UTC()}
	require.NoError(t, bus.Publish(t.Context(), &events.AlertOpened{Alert: alert, Recipients: recipients}))
	resolved := time.Now().UTC()
	alert.ResolvedAt = &resolved
	alert.Resolution = "Node connected"
	require.NoError(t, bus.Publish(t.Context(), &events.AlertResolved{Alert: alert}))
	drain(t, bus)
	store := NewStore(db)
	defaultLink, err := store.Get(t.Context(), alert.ID, recipients[0])
	require.NoError(t, err)
	assert.Equal(t, int64(101), defaultLink.OpenedMessageID.Int64)
	assert.True(t, defaultLink.ResolvedAt.Valid)
	assert.Equal(t, int64(102), defaultLink.ReplyMessageID.Int64)
	assert.Equal(t, 2, counts["-100"])
	infraLink, err := store.Get(t.Context(), alert.ID, recipients[1])
	require.NoError(t, err)
	assert.False(t, infraLink.OpenedAt.Valid)
	assert.False(t, infraLink.ResolvedAt.Valid)
	require.NoError(t, db.Close())
	db, err = storage.Open(t.Context(), directory)
	require.NoError(t, err)
	defer db.Close()
	bus = outbox.New(db)
	subscribeTestRecipients(t, bus, db, spec, server.URL)
	failInfra.Store(false)
	var infraOpenEventID string
	require.NoError(t, db.Get(t.Context(), &infraOpenEventID, `SELECT d.event_id FROM outbox_deliveries d
		JOIN outbox_events e ON e.id=d.event_id WHERE e.event_type='alertOpened' AND d.subscription_id=?`,
		SubscriptionID(recipients[1])))
	_, err = db.Exec(t.Context(), `UPDATE outbox_deliveries SET status='failed' WHERE event_id=? AND subscription_id=?`,
		infraOpenEventID, SubscriptionID(recipients[1]))
	require.NoError(t, err)
	require.NoError(t, bus.Replay(t.Context(), infraOpenEventID, SubscriptionID(recipients[1])))
	drain(t, bus)
	infraLink, err = NewStore(db).Get(t.Context(), alert.ID, recipients[1])
	require.NoError(t, err)
	assert.Equal(t, int64(201), infraLink.OpenedMessageID.Int64)
	assert.Equal(t, int64(202), infraLink.ReplyMessageID.Int64)
	assert.Equal(t, []int64{201}, infraReplies)
	assert.Equal(t, 2, counts["-100"], "successful chat must not be sent again")
	assert.Equal(t, 2, counts["-101"])
}

func TestAlertMessageStoreRetentionAndReplay(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	spec := alertSpec()
	recipients := spec.AlertRecipients("node_disconnected")
	store := NewStore(db)
	for _, recipient := range recipients {
		require.NoError(t, store.Ensure(t.Context(), "old-open", recipient))
		require.NoError(t, store.MarkOpened(t.Context(), "old-open", recipient, notifiers.Receipt{MessageID: int64(recipient.ThreadID)}, time.Now().Add(-365*24*time.Hour)))
	}
	require.NoError(t, store.PruneCompleted(t.Context()))
	for _, recipient := range recipients {
		link, getErr := store.Get(t.Context(), "old-open", recipient)
		require.NoError(t, getErr)
		assert.True(t, link.OpenedAt.Valid, "open alerts have no TTL")
	}
	require.NoError(t, store.MarkResolved(t.Context(), "old-open", recipients[0], notifiers.Receipt{MessageID: 55, Action: notifiers.ActionReply}, time.Now()))
	require.NoError(t, store.PruneCompleted(t.Context()))
	_, err = store.Get(t.Context(), "old-open", recipients[0])
	assert.ErrorIs(t, err, sql.ErrNoRows)
	_, err = store.Get(t.Context(), "old-open", recipients[1])
	assert.NoError(t, err)
}

func TestAlertReceiptSurvivesReplayBeforeAcknowledgement(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	spec := alertSpec()
	recipient := spec.AlertRecipients("node_disconnected")[0]
	sends := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends++
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer server.Close()
	bus := outbox.New(db)
	subscriber := &Subscriber{store: NewStore(db), spec: spec, recipient: recipient, now: time.Now, apiBaseURL: server.URL}
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, SubscriptionID(recipient), outbox.External(subscriber)))
	alert := events.AlertSnapshot{ID: "crash-cycle", Kind: "node_disconnected", Title: "Node disconnected", OpenedAt: time.Now().UTC()}
	event := &events.AlertOpened{Alert: alert, Recipients: []events.AlertRecipient{recipient}}
	require.NoError(t, bus.Publish(t.Context(), event))
	// Simulate a process that stored the Telegram receipt and crashed before ACK.
	require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: event}))
	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	assert.Equal(t, 1, sends)
	link, err := NewStore(db).Get(t.Context(), alert.ID, recipient)
	require.NoError(t, err)
	assert.Equal(t, int64(42), link.OpenedMessageID.Int64)
}

func TestAlertResolutionUsesOriginalPhysicalChatAfterRouteChange(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	oldSpec := alertSpec()
	recipient := oldSpec.AlertRecipients("node_disconnected")[1]
	store := NewStore(db)
	require.NoError(t, store.Ensure(t.Context(), "moved-route", recipient))
	require.NoError(t, store.MarkOpened(t.Context(), "moved-route", recipient,
		notifiers.Receipt{MessageID: 77}, time.Now()))
	newSpec := alertSpec()
	newSpec.Alerts["node_disconnected"] = config.NotificationAlertRoutes{Telegram: []config.TelegramAlertChannel{{
		Chats: config.NotificationChatNames{"default"},
	}}}
	var chatID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode Telegram request: %v", err)
			return
		}
		chatID = body.ChatID
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":78}}`))
	}))
	defer server.Close()
	subscriber := &Subscriber{store: store, spec: newSpec, recipient: recipient, now: time.Now, apiBaseURL: server.URL}
	resolved := time.Now().UTC()
	err = subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertResolved{Alert: events.AlertSnapshot{
		ID: "moved-route", Kind: "node_disconnected", Title: "Node disconnected",
		OpenedAt: resolved.Add(-time.Minute), ResolvedAt: &resolved, Resolution: "Node connected",
	}}})
	require.NoError(t, err)
	assert.Equal(t, "-101", chatID)
	link, err := store.Get(t.Context(), "moved-route", recipient)
	require.NoError(t, err)
	assert.Equal(t, int64(78), link.ReplyMessageID.Int64)
	require.NoError(t, store.Ensure(t.Context(), "no-original", recipient))
	require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertResolved{Alert: events.AlertSnapshot{
		ID: "no-original", Kind: "node_disconnected", Title: "Node disconnected",
		OpenedAt: resolved.Add(-time.Minute), ResolvedAt: &resolved, Resolution: "Node connected",
	}}}))
	noOriginal, err := store.Get(t.Context(), "no-original", recipient)
	require.NoError(t, err)
	assert.Equal(t, "send", noOriginal.ResolvedAction.String)
	assert.False(t, noOriginal.ReplyMessageID.Valid)
	defaultRecipient := newSpec.AlertRecipients("node_disconnected")[0]
	defaultSubscriber := &Subscriber{
		store: store, spec: newSpec, recipient: defaultRecipient, now: time.Now, apiBaseURL: server.URL,
	}
	require.NoError(t, defaultSubscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertResolved{
		Alert: events.AlertSnapshot{ID: "never-opened", Kind: "node_disconnected", Title: "Node disconnected",
			OpenedAt: resolved.Add(-time.Minute), ResolvedAt: &resolved, Resolution: "Node connected"},
		Recipients: []events.AlertRecipient{defaultRecipient},
	}}))
	assert.Equal(t, "-100", chatID)
	defaultLink, err := store.Get(t.Context(), "never-opened", defaultRecipient)
	require.NoError(t, err)
	assert.Equal(t, "send", defaultLink.ResolvedAction.String)

	newSpec.Messengers.Telegram.BotToken.Content = nil
	missing := &Subscriber{store: store, spec: newSpec, recipient: recipient, now: time.Now, apiBaseURL: server.URL}
	alert := events.AlertSnapshot{ID: "missing-credentials", Kind: "node_disconnected", Title: "Node disconnected"}
	require.ErrorContains(t, missing.Handle(t.Context(), events.Envelope{Event: &events.AlertOpened{
		Alert: alert, Recipients: []events.AlertRecipient{recipient},
	}}), "credentials unavailable")
}


func TestResolvedAlertDoesNotSendOpeningOnManualReplay(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	spec := alertSpec()
	recipient := spec.AlertRecipients("node_disconnected")[0]
	var sends int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends++
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer server.Close()

	bus := outbox.New(db)
	subscriber := &Subscriber{
		store: NewStore(db), spec: spec, recipient: recipient, now: time.Now, apiBaseURL: server.URL,
	}
	id := SubscriptionID(recipient)
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, id, outbox.External(subscriber)))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, id, outbox.External(subscriber)))

	alert := events.AlertSnapshot{
		ID: "resolved-before-replay", Kind: "node_disconnected",
		Title: "Node disconnected", OpenedAt: time.Now().UTC(),
	}
	require.NoError(t, bus.Publish(t.Context(), &events.AlertOpened{
		Alert: alert, Recipients: []events.AlertRecipient{recipient},
	}))

	var openingID string
	require.NoError(t, db.Get(t.Context(), &openingID, `SELECT e.id FROM outbox_events e
		JOIN outbox_deliveries d ON d.event_id = e.id
		WHERE e.event_type = 'alertOpened' AND d.subscription_id = ?`, id))
	// Simulate an opening delivery exhausted before it could send.
	_, err = db.Exec(t.Context(), `UPDATE outbox_deliveries SET status='failed'
		WHERE event_id=? AND subscription_id=?`, openingID, id)
	require.NoError(t, err)

	resolved := time.Now().UTC()
	alert.ResolvedAt = &resolved
	alert.Resolution = "Node reconnected"
	require.NoError(t, bus.Publish(t.Context(), &events.AlertResolved{
		Alert: alert, Recipients: []events.AlertRecipient{recipient},
	}))
	drain(t, bus)
	require.Equal(t, 1, sends, "resolution without a delivered opening must send once")

	store := NewStore(db)
	link, err := store.Get(t.Context(), alert.ID, recipient)
	require.NoError(t, err)
	require.True(t, link.ResolvedAt.Valid)
	require.False(t, link.OpenedAt.Valid)
	require.Equal(t, "send", link.ResolvedAction.String)

	require.NoError(t, bus.Replay(t.Context(), openingID, id))
	drain(t, bus)
	assert.Equal(t, 1, sends, "replayed opening must not send after resolved notification")
	link, err = store.Get(t.Context(), alert.ID, recipient)
	require.NoError(t, err)
	assert.False(t, link.OpenedAt.Valid)
	assert.True(t, link.ResolvedAt.Valid)

	// The SQL guard must also reject a late receipt even without the subscriber check.
	require.NoError(t, store.MarkOpened(t.Context(), alert.ID, recipient,
		notifiers.Receipt{MessageID: 999, Action: notifiers.ActionSend}, time.Now()))
	link, err = store.Get(t.Context(), alert.ID, recipient)
	require.NoError(t, err)
	assert.False(t, link.OpenedAt.Valid)
}
