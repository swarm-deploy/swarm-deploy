package alerts

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func alertSpec() config.NotificationSpec {
	return config.NotificationSpec{
		Messengers: config.NotificationMessengersSpec{Telegram: config.NotificationTelegramSpec{
			BotToken: specw.File{Content: []byte("123:token")}, Retries: 1,
			Chats: map[string]config.NotificationChatSpec{"default": {ID: "-100", ThreadID: 1}, "infra": {ID: "-101", ThreadID: 2}},
		}},
		Alerts: map[string]config.NotificationAlertRoutes{"NodeDisconnected": {Telegram: []config.TelegramAlertChannel{{
			Chats:    config.NotificationChatNames{"default", "infra"},
			Opened:   config.AlertMessageSpec{Message: "{{ .Title }}"},
			Resolved: config.AlertMessageSpec{Action: "reply", Message: "{{ .Resolution }}"},
		}}}},
	}
}

func TestTelegramAlertDeliveryReportsMessengerHealth(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	spec := alertSpec()
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	alertStore := modelstore.NewSQLStore(db)
	projection := alertmanagement.NewSQLSubscriber(alertStore, bus,
		func(model.AlertKind) []events.AlertRecipient { return nil }, &config.Config{})
	require.NoError(t, eventbus.SubscribeMany(bus, []events.TypeName{
		events.TypeNameMessengerAvailable, events.TypeNameMessengerUnavailable,
		events.TypeNameMessengerMisconfigured, events.TypeNameMessengerDeliverySucceeded,
	}, "alert-management", projection))

	var reject atomic.Bool
	reject.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if reject.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	t.Cleanup(server.Close)
	recipients := spec.AlertRecipients("sync_failed")
	subscriber := &Subscriber{
		store: NewStore(db), outbox: outbox.New(db), db: db, spec: spec, recipient: recipients[0],
		publisher: bus, now: time.Now, apiBaseURL: server.URL,
	}
	alert := events.AlertSnapshot{ID: "sync", Kind: "sync_failed", Title: "Sync failed"}
	require.ErrorContains(t, subscriber.Handle(ctx, events.Envelope{Event: &events.AlertOpened{
		Alert: alert, Recipients: recipients,
	}}), "telegram API status 401")
	drain(t, bus)
	alerts, err := alertStore.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertKindMessengerMisconfigured, alerts[0].Kind)
	assert.Equal(t, model.AlertStatusOpen, alerts[0].Status)
	assert.Equal(t, "telegram:default", alerts[0].ResourceID)
	assert.Equal(t, "invalid_credentials", alerts[0].ErrorCode)

	messengerAlert := events.AlertSnapshot{
		ID: "other-chat", Kind: "messenger_misconfigured", ResourceID: "telegram:infra",
		Title: "Messenger misconfigured",
	}
	require.ErrorContains(t, subscriber.Handle(ctx, events.Envelope{Event: &events.AlertOpened{
		Alert: messengerAlert, Recipients: recipients,
	}}), "telegram API status 401")
	handled, err := bus.ProcessNext(ctx)
	require.NoError(t, err)
	assert.False(t, handled, "messenger health alerts must suppress their own observations")

	reject.Store(false)
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{Event: &events.AlertResolved{Alert: alert}}))
	drain(t, bus)
	recovered, err := alertStore.Get(ctx, alerts[0].ID)
	require.NoError(t, err)
	assert.Equal(t, model.AlertStatusResolved, recovered.Status)
}

func TestAlertTelegramButtons(t *testing.T) {
	const inheritedURL = "https://example.com/current/dep-2"
	const resolvedURL = "https://example.com/resolved/dep-2"
	resolvedButtons := []notificationtemplate.ButtonSpec{{
		Text: "{{ .Resolution }}", URL: "https://example.com/resolved/{{ .DeploymentID }}",
	}}
	for _, tt := range []struct {
		name, action, wantURL string
		buttons               []notificationtemplate.ButtonSpec
		invalidEdit           bool
	}{
		{name: "edit rerenders current opening template", action: "edit", wantURL: inheritedURL},
		{name: "edit uses explicit resolution buttons", action: "edit", buttons: resolvedButtons, wantURL: resolvedURL},
		{name: "edit clears buttons with an empty list", action: "edit", buttons: []notificationtemplate.ButtonSpec{}},
		{name: "reply does not inherit", action: "reply"},
		{name: "reply uses resolution buttons", action: "reply", buttons: resolvedButtons, wantURL: resolvedURL},
		{name: "edit fallback drops inherited buttons", action: "edit", wantURL: inheritedURL, invalidEdit: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			type request struct {
				Path   string
				Markup *struct {
					Rows [][]notificationtemplate.Button `json:"inline_keyboard"`
				} `json:"reply_markup"`
			}
			requests := make(chan request, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := request{Path: r.URL.Path}
				if assert.NoError(t, json.UnmarshalRead(r.Body, &body)) {
					requests <- body
				}
				if tt.invalidEdit && strings.HasSuffix(r.URL.Path, "/editMessageText") {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"ok":false,"description":"message to edit not found"}`))
					return
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":71}}`))
			}))
			t.Cleanup(server.Close)
			db, err := storage.OpenSQLite(t.Context(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			spec := alertSpec()
			routes := spec.Alerts["NodeDisconnected"]
			spec.Alerts["SyncFailed"] = routes
			route := &routes.Telegram[0]
			route.Opened.Buttons = []notificationtemplate.ButtonSpec{{Text: "Open", URL: "https://example.com/{{ .DeploymentID }}"}}
			route.Resolved.Action = tt.action
			route.Resolved.Buttons = tt.buttons
			recipients := spec.AlertRecipients("sync_failed")
			subscriber := &Subscriber{
				store: NewStore(db), outbox: outbox.New(db), db: db,
				spec: spec, recipient: recipients[0], publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
			}
			alert := events.AlertSnapshot{ID: "alert-1", Kind: "sync_failed", Title: "Sync failed", DeploymentID: "dep-1"}
			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertOpened{
				Alert: alert, Recipients: recipients,
			}}))
			require.Len(t, requests, 1)
			opened := <-requests
			assert.True(t, strings.HasSuffix(opened.Path, "/sendMessage"))
			require.NotNil(t, opened.Markup)
			assert.Equal(t, [][]notificationtemplate.Button{{{Text: "Open", URL: "https://example.com/dep-1"}}}, opened.Markup.Rows)

			route.Opened.Buttons[0].URL = "https://example.com/current/{{ .DeploymentID }}"
			alert.DeploymentID = "dep-2"
			alert.Resolution = "Recovered"
			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertResolved{Alert: alert}}))
			if tt.invalidEdit {
				require.Len(t, requests, 2)
			} else {
				require.Len(t, requests, 1)
			}
			resolved := <-requests
			if tt.action == "edit" {
				assert.True(t, strings.HasSuffix(resolved.Path, "/editMessageText"))
			} else {
				assert.True(t, strings.HasSuffix(resolved.Path, "/sendMessage"))
			}
			if tt.wantURL == "" {
				assert.Nil(t, resolved.Markup)
			} else {
				require.NotNil(t, resolved.Markup)
				require.Len(t, resolved.Markup.Rows, 1)
				require.Len(t, resolved.Markup.Rows[0], 1)
				assert.Equal(t, tt.wantURL, resolved.Markup.Rows[0][0].URL)
			}
			if tt.invalidEdit {
				fallback := <-requests
				assert.True(t, strings.HasSuffix(fallback.Path, "/sendMessage"))
				assert.Nil(t, fallback.Markup)
			}
		})
	}
}

func TestSyncFailedAlertRendering(t *testing.T) {
	tests := []struct {
		name          string
		title         string
		customMessage string
		wantText      string
		wantParseMode string
	}{
		{
			name:  "default multiline HTML",
			title: "Synchronization failed",
			wantText: `<b>🚨 Synchronization failed</b>

<b>Stack:</b> <code>infra&lt;&amp;</code>

<b>Error:</b>
<pre>first line
&lt;broken&gt; &amp; stopped</pre>`,
			wantParseMode: "HTML",
		},
		{
			name:  "interrupted deployment title",
			title: "Synchronization outcome unknown",
			wantText: `<b>🚨 Synchronization outcome unknown</b>

<b>Stack:</b> <code>infra&lt;&amp;</code>

<b>Error:</b>
<pre>first line
&lt;broken&gt; &amp; stopped</pre>`,
			wantParseMode: "HTML",
		},
		{
			name:  "escape dynamic title",
			title: "Deployment <outcome> & unknown",
			wantText: `<b>🚨 Deployment &lt;outcome&gt; &amp; unknown</b>

<b>Stack:</b> <code>infra&lt;&amp;</code>

<b>Error:</b>
<pre>first line
&lt;broken&gt; &amp; stopped</pre>`,
			wantParseMode: "HTML",
		},
		{
			name:          "custom template",
			title:         "Synchronization failed",
			customMessage: "custom {{ .ResourceID }} deployment {{ .DeploymentID }}: {{ .Message }}",
			wantText:      "custom infra<& deployment deployment-1: first line\n<broken> & stopped",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := storage.OpenSQLite(t.Context(), t.TempDir())
			require.NoError(t, err)
			defer db.Close()
			spec := alertSpec()
			if test.customMessage != "" {
				spec.Alerts["SyncFailed"] = config.NotificationAlertRoutes{Telegram: []config.TelegramAlertChannel{{
					Opened: config.AlertMessageSpec{Message: test.customMessage},
				}}}
			}
			var request struct {
				Text      string `json:"text"`
				ParseMode string `json:"parse_mode"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.UnmarshalRead(r.Body, &request))
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
			}))
			defer server.Close()
			recipient := spec.AlertRecipients("sync_failed")[0]
			subscriber := &Subscriber{
				store: NewStore(db), outbox: outbox.New(db), db: db,
				spec: spec, recipient: recipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
			}
			alert := events.AlertSnapshot{
				ID: "failed", Kind: "sync_failed", Title: test.title, ResourceID: "infra<&",
				DeploymentID: "deployment-1", Message: "first line\n<broken> & stopped", OpenedAt: time.Now().UTC(),
			}
			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertOpened{
				Alert: alert, Recipients: []events.AlertRecipient{recipient},
			}}))
			assert.Equal(t, test.wantText, request.Text)
			assert.Equal(t, test.wantParseMode, request.ParseMode)
		})
	}
}

func TestSyncFailedDefaultAlertTruncatesLongError(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	spec := alertSpec()
	var request struct {
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.UnmarshalRead(r.Body, &request))
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer server.Close()
	recipient := spec.AlertRecipients("sync_failed")[0]
	subscriber := &Subscriber{
		store: NewStore(db), outbox: outbox.New(db), db: db,
		spec: spec, recipient: recipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
	}
	alert := events.AlertSnapshot{
		ID: "long-failure", Kind: "sync_failed", Title: "Synchronization outcome unknown", ResourceID: "infra-monitoring",
		Message: strings.Repeat("failed <&> 😀\n", 500), OpenedAt: time.Now().UTC(),
	}
	require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertOpened{
		Alert: alert, Recipients: []events.AlertRecipient{recipient},
	}}))
	visible := html.UnescapeString(strings.NewReplacer(
		"<b>", "", "</b>", "", "<code>", "", "</code>", "", "<pre>", "", "</pre>", "",
	).Replace(request.Text))
	assert.LessOrEqual(t, telegramTextLength(visible), telegramMessageLimit)
	assert.Contains(t, request.Text, "<b>🚨 Synchronization outcome unknown</b>")
	assert.Contains(t, request.Text, "[error truncated]")
	assert.Equal(t, "HTML", request.ParseMode)
}

func subscribeTestRecipients(t *testing.T, bus *eventbus.OutboxDispatcher, db storage.Database, spec config.NotificationSpec, url string) []events.AlertRecipient {
	t.Helper()
	recipients := spec.AlertRecipients("node_disconnected")
	for _, recipient := range recipients {
		subscriber := eventbus.External(&Subscriber{
			store: NewStore(db), outbox: outbox.New(db), db: db,
			spec: spec, recipient: recipient, publisher: bus, now: time.Now, apiBaseURL: url,
		})
		require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, SubscriptionID(recipient), subscriber))
		require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, SubscriptionID(recipient), subscriber))
	}
	return recipients
}

func drain(t *testing.T, bus *eventbus.OutboxDispatcher) {
	t.Helper()
	for range 12 {
		handled, err := bus.ProcessNext(t.Context())
		require.NoError(t, err)
		if !handled {
			return
		}
	}
}

func deliveryEventID(
	t *testing.T, repository *outbox.Repository, eventType events.TypeName, subscriptionID string,
) string {
	t.Helper()
	deliveries, err := repository.ListDeliveries(t.Context(), outbox.ListDeliveriesFilter{
		SubscriptionIDPrefix: subscriptionID,
	})
	require.NoError(t, err)
	for _, delivery := range deliveries {
		if delivery.EventType == eventType && delivery.SubscriptionID == subscriptionID {
			return delivery.EventID
		}
	}
	require.FailNow(t, "delivery not found", "event type %s subscription %s", eventType, subscriptionID)
	return ""
}

func TestRegisterIncludesFallbackAndCustomRecipientsForFutureAlertKinds(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	spec := alertSpec()
	spec.Messengers.Telegram.Chats["deployments"] = config.NotificationChatSpec{ID: "-102", ThreadID: 3}
	spec.Alerts = map[string]config.NotificationAlertRoutes{
		"default": {Telegram: []config.TelegramAlertChannel{{Chats: config.NotificationChatNames{"default"}}}},
		"SyncFailed": {Telegram: []config.TelegramAlertChannel{{
			Chats: config.NotificationChatNames{"deployments"},
		}}},
		"NodeDisconnected": {Telegram: []config.TelegramAlertChannel{{
			Chats: config.NotificationChatNames{"infra"},
		}}},
	}
	repository := outbox.New(db)
	bus := eventbus.NewOutboxDispatcher(repository)
	require.NoError(t, Register(t.Context(), bus, repository, db, spec))
	futureRecipients := spec.AlertRecipients("future_alert_kind")
	require.Len(t, futureRecipients, 1)
	require.NoError(t, bus.Publish(t.Context(), &events.AlertOpened{
		Alert: events.AlertSnapshot{
			ID: "future-alert", Kind: "future_alert_kind", Title: "Future alert", OpenedAt: time.Now().UTC(),
		},
		Recipients: futureRecipients,
	}))

	for _, kind := range []string{"future_alert_kind", "sync_failed", "node_disconnected"} {
		recipients := spec.AlertRecipients(kind)
		require.Len(t, recipients, 1)
		assert.NotEmpty(t, deliveryEventID(
			t, repository, events.TypeNameAlertOpened, SubscriptionID(recipients[0]),
		), "recipient for %s must be registered", kind)
	}
}

func TestRegisterRecoversSubscriberFromRetainedDelivery(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repository := outbox.New(db)
	bus := eventbus.NewOutboxDispatcher(repository)
	recipient := events.AlertRecipient{BotIdentity: "retained-bot", ChatID: "-900", ThreadID: 7}
	require.NoError(t, repository.Publish(t.Context(), &events.AlertOpened{
		Alert: events.AlertSnapshot{
			ID: "retained-subscription", Kind: "node_disconnected", OpenedAt: time.Now().UTC(),
		},
	}, []string{SubscriptionID(recipient)}, time.Now().UTC()))

	require.NoError(t, Register(t.Context(), bus, repository, db, config.NotificationSpec{}))
	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	deliveries, err := repository.ListDeliveries(t.Context(), outbox.ListDeliveriesFilter{})
	require.NoError(t, err)
	assert.Empty(t, deliveries)
}

func TestAlertIndependentDeliveryOrderingAndRestart(t *testing.T) {
	directory := t.TempDir()
	db, err := storage.OpenSQLite(t.Context(), directory)
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
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
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
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
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
	db, err = storage.OpenSQLite(t.Context(), directory)
	require.NoError(t, err)
	defer db.Close()
	repository := outbox.New(db)
	bus = eventbus.NewOutboxDispatcher(repository)
	subscribeTestRecipients(t, bus, db, spec, server.URL)
	failInfra.Store(false)
	infraOpenEventID := deliveryEventID(
		t, repository, events.TypeNameAlertOpened, SubscriptionID(recipients[1]),
	)
	_, err = db.Exec(t.Context(), `UPDATE outbox_deliveries SET status='failed' WHERE event_id=? AND subscription_id=?`,
		infraOpenEventID, SubscriptionID(recipients[1]))
	require.NoError(t, err)
	require.NoError(t, repository.Replay(t.Context(), infraOpenEventID, SubscriptionID(recipients[1]), time.Now()))
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
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	spec := alertSpec()
	recipients := spec.AlertRecipients("node_disconnected")
	store := NewStore(db)
	repository := outbox.New(db)
	for _, recipient := range recipients {
		require.NoError(t, store.Ensure(t.Context(), "old-open", recipient))
		require.NoError(t, store.MarkOpened(t.Context(), "old-open", recipient, notifiers.Receipt{MessageID: int64(recipient.ThreadID)}, time.Now().Add(-365*24*time.Hour)))
	}
	require.NoError(t, pruneCompleted(t.Context(), db, store, repository))
	for _, recipient := range recipients {
		link, getErr := store.Get(t.Context(), "old-open", recipient)
		require.NoError(t, getErr)
		assert.True(t, link.OpenedAt.Valid, "open alerts have no TTL")
	}
	require.NoError(t, store.MarkResolved(t.Context(), "old-open", recipients[0], notifiers.Receipt{MessageID: 55, Action: notifiers.ActionReply}, time.Now()))
	require.NoError(t, pruneCompleted(t.Context(), db, store, repository))
	_, err = store.Get(t.Context(), "old-open", recipients[0])
	assert.ErrorIs(t, err, sql.ErrNoRows)
	_, err = store.Get(t.Context(), "old-open", recipients[1])
	assert.NoError(t, err)
}

func TestPruneCompletedWaitsForEveryRetainedLifecycleDeliveryState(t *testing.T) {
	for _, status := range []outbox.DeliveryStatus{
		outbox.DeliveryStatusPending,
		outbox.DeliveryStatusProcessing,
		outbox.DeliveryStatusFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			db, err := storage.OpenSQLite(t.Context(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			repository := outbox.New(db)
			store := NewStore(db)
			recipient := alertSpec().AlertRecipients("node_disconnected")[0]
			const alertID = "retained-lifecycle"
			const subscriptionID = "test-alert-delivery"
			now := time.Now().UTC()
			require.NoError(t, store.Ensure(t.Context(), alertID, recipient))
			require.NoError(t, store.MarkResolved(
				t.Context(), alertID, recipient, notifiers.Receipt{Action: notifiers.ActionSend}, now,
			))
			require.NoError(t, repository.Publish(t.Context(), &events.AlertOpened{
				Alert:      events.AlertSnapshot{ID: alertID, Kind: "node_disconnected", OpenedAt: now},
				Recipients: []events.AlertRecipient{recipient},
			}, []string{subscriptionID}, now))

			var claimed outbox.Delivery
			if status != outbox.DeliveryStatusPending {
				claimed, err = repository.Claim(t.Context(), now, time.Hour)
				require.NoError(t, err)
			}
			if status == outbox.DeliveryStatusFailed {
				require.NoError(t, repository.Fail(t.Context(), claimed, "test", true, 1, now))
			}

			require.NoError(t, pruneCompleted(t.Context(), db, store, repository))
			_, err = store.Get(t.Context(), alertID, recipient)
			require.NoError(t, err, "retained %s publication must protect the link", status)

			switch status {
			case outbox.DeliveryStatusPending:
				claimed, err = repository.Claim(t.Context(), now, time.Hour)
				require.NoError(t, err)
				require.NoError(t, repository.Acknowledge(t.Context(), claimed, now))
			case outbox.DeliveryStatusProcessing:
				require.NoError(t, repository.Acknowledge(t.Context(), claimed, now))
			case outbox.DeliveryStatusFailed:
				require.NoError(t, repository.Discard(
					t.Context(), claimed.EventID, claimed.SubscriptionID, now,
				))
			}

			require.NoError(t, pruneCompleted(t.Context(), db, store, repository))
			_, err = store.Get(t.Context(), alertID, recipient)
			assert.ErrorIs(t, err, sql.ErrNoRows)
		})
	}
}

func TestRegisterPrunesMoreCompletedAlertsThanSQLiteParameterLimit(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(ctx, `WITH RECURSIVE ids(n) AS (
		VALUES(1) UNION ALL SELECT n + 1 FROM ids WHERE n < 32768
	)
	INSERT INTO notification_messages (alert_id, messenger, bot_identity, chat_id, resolved_at)
	SELECT printf('alert-%05d', n), 'telegram', 'bot', 'chat', 1 FROM ids`)
	require.NoError(t, err)

	repository := outbox.New(db)
	now := time.Now().UTC()
	const retainedID = "alert-32768"
	require.NoError(t, repository.Publish(ctx, &events.AlertResolved{
		Alert: events.AlertSnapshot{ID: retainedID, Kind: "sync_failed", OpenedAt: now},
	}, []string{"subscriber"}, now))

	require.NoError(t, Register(ctx, eventbus.NewOutboxDispatcher(repository), repository, db, config.NotificationSpec{}))
	remaining, err := NewStore(db).CompletedAlertIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{retainedID}, remaining)
}

func TestPruneCompletedKeepsUnfinishedLinks(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewStore(db)
	recipient := alertSpec().AlertRecipients("node_disconnected")[0]
	require.NoError(t, store.Ensure(t.Context(), "unfinished", recipient))

	require.NoError(t, pruneCompleted(t.Context(), db, store, outbox.New(db)))
	_, err = store.Get(t.Context(), "unfinished", recipient)
	require.NoError(t, err)
}

func TestPruneCompletedKeepsLinksWhenOutboxInspectionFails(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewStore(db)
	recipient := alertSpec().AlertRecipients("node_disconnected")[0]
	require.NoError(t, store.Ensure(t.Context(), "inspection-error", recipient))
	require.NoError(t, store.MarkResolved(
		t.Context(), "inspection-error", recipient, notifiers.Receipt{Action: notifiers.ActionSend}, time.Now(),
	))

	outboxDB, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	repository := outbox.New(outboxDB)
	require.NoError(t, outboxDB.Close())
	require.Error(t, pruneCompleted(t.Context(), db, store, repository))

	_, err = store.Get(t.Context(), "inspection-error", recipient)
	require.NoError(t, err)
}

func TestEarlyResolutionUsesRetainedOpeningRecipients(t *testing.T) {
	testCases := []struct {
		name                 string
		retainOpening        bool
		subscriberRecipient  int
		resolutionRecipients []int
		wantSends            int
	}{
		{
			name: "recipient from retained opening receives resolution", retainOpening: true,
			subscriberRecipient: 0, resolutionRecipients: []int{0, 1}, wantSends: 1,
		},
		{
			name: "recipient absent from retained opening is skipped", retainOpening: true,
			subscriberRecipient: 1, resolutionRecipients: []int{0, 1}, wantSends: 0,
		},
		{
			name:                "resolution recipients are fallback after opening publication is gone",
			subscriberRecipient: 1, resolutionRecipients: []int{1}, wantSends: 1,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db, err := storage.OpenSQLite(t.Context(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			repository := outbox.New(db)
			spec := alertSpec()
			recipients := spec.AlertRecipients("node_disconnected")
			alert := events.AlertSnapshot{
				ID: "early-resolution", Kind: "node_disconnected", Title: "Node disconnected",
				OpenedAt: time.Now().UTC(),
			}
			if testCase.retainOpening {
				require.NoError(t, repository.Publish(t.Context(), &events.AlertOpened{
					Alert: alert, Recipients: []events.AlertRecipient{recipients[0]},
				}, []string{"opening-delivery"}, time.Now().UTC()))
			}
			resolvedRecipients := make([]events.AlertRecipient, 0, len(testCase.resolutionRecipients))
			for _, index := range testCase.resolutionRecipients {
				resolvedRecipients = append(resolvedRecipients, recipients[index])
			}
			sends := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				sends++
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
			}))
			t.Cleanup(server.Close)
			subscriber := &Subscriber{
				store: NewStore(db), outbox: repository, db: db,
				spec: spec, recipient: recipients[testCase.subscriberRecipient],
				publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
			}
			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertResolved{
				Alert: alert, Recipients: resolvedRecipients,
			}}))
			assert.Equal(t, testCase.wantSends, sends)
		})
	}
}

func TestAlertReceiptSurvivesReplayBeforeAcknowledgement(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
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
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	subscriber := &Subscriber{
		store: NewStore(db), outbox: outbox.New(db), db: db,
		spec: spec, recipient: recipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
	}
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, SubscriptionID(recipient), eventbus.External(subscriber)))
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
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
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
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Errorf("decode Telegram request: %v", err)
			return
		}
		chatID = body.ChatID
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":78}}`))
	}))
	defer server.Close()
	repository := outbox.New(db)
	subscriber := &Subscriber{
		store: store, outbox: repository, db: db,
		spec: newSpec, recipient: recipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
	}
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
		store: store, outbox: repository, db: db,
		spec: newSpec, recipient: defaultRecipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
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
	missing := &Subscriber{
		store: store, outbox: repository, db: db,
		spec: newSpec, recipient: recipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
	}
	alert := events.AlertSnapshot{ID: "missing-credentials", Kind: "node_disconnected", Title: "Node disconnected"}
	require.ErrorContains(t, missing.Handle(t.Context(), events.Envelope{Event: &events.AlertOpened{
		Alert: alert, Recipients: []events.AlertRecipient{recipient},
	}}), "credentials unavailable")
}

func TestResolvedAlertDoesNotSendOpeningOnManualReplay(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
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

	repository := outbox.New(db)
	bus := eventbus.NewOutboxDispatcher(repository)
	subscriber := &Subscriber{
		store: NewStore(db), outbox: repository, db: db,
		spec: spec, recipient: recipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
	}
	id := SubscriptionID(recipient)
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, id, eventbus.External(subscriber)))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, id, eventbus.External(subscriber)))

	alert := events.AlertSnapshot{
		ID: "resolved-before-replay", Kind: "node_disconnected",
		Title: "Node disconnected", OpenedAt: time.Now().UTC(),
	}
	require.NoError(t, bus.Publish(t.Context(), &events.AlertOpened{
		Alert: alert, Recipients: []events.AlertRecipient{recipient},
	}))

	openingID := deliveryEventID(t, repository, events.TypeNameAlertOpened, id)
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

	require.NoError(t, repository.Replay(t.Context(), openingID, id, time.Now()))
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

func TestGitAlertDefaultTelegramHTMLAndCustomTemplates(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom=%t", custom), func(t *testing.T) {
			db, err := storage.OpenSQLite(t.Context(), t.TempDir())
			require.NoError(t, err)
			defer db.Close()
			spec := alertSpec()
			buttons := []notificationtemplate.ButtonSpec{{
				Text: "{{ .ResourceID }}",
				URL:  "https://example.com/repositories?repo={{ .ResourceID | urlquery }}",
			}}
			route := config.TelegramAlertChannel{
				Opened:   config.AlertMessageSpec{Buttons: buttons},
				Resolved: config.AlertMessageSpec{Buttons: buttons},
			}
			if custom {
				route.Opened.Message = "repo={{ .ResourceID }}"
				route.Resolved.Message = "recovered={{ .ResourceID }}"
			}
			spec.Alerts["GitRepositoryUnavailable"] = config.NotificationAlertRoutes{
				Telegram: []config.TelegramAlertChannel{route},
			}
			type request struct {
				Text      string `json:"text"`
				ParseMode string `json:"parse_mode"`
				Markup    struct {
					Rows [][]notificationtemplate.Button `json:"inline_keyboard"`
				} `json:"reply_markup"`
			}
			var received []request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req request
				require.NoError(t, json.UnmarshalRead(r.Body, &req))
				received = append(received, req)
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
			}))
			defer server.Close()
			recipient := spec.AlertRecipients("git_repository_unavailable")[0]
			subscriber := &Subscriber{
				store: NewStore(db), outbox: outbox.New(db), db: db,
				spec: spec, recipient: recipient, publisher: &eventbus.NopDispatcher{}, now: time.Now, apiBaseURL: server.URL,
			}
			alert := events.AlertSnapshot{
				ID: "git-html", Kind: "git_repository_unavailable", Title: "Git repository unavailable",
				ResourceID: "example.com/org/<repo>&test", Message: "Git <network> & unavailable",
				ErrorCode: "network", Occurrences: 3, Duration: "1m19s", OpenedAt: time.Now().UTC(),
			}
			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertOpened{
				Alert: alert, Recipients: []events.AlertRecipient{recipient},
			}}))
			resolved := time.Now().UTC()
			alert.ResolvedAt = &resolved
			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: &events.AlertResolved{
				Alert: alert, Recipients: []events.AlertRecipient{recipient},
			}}))
			require.Len(t, received, 2)
			for _, req := range received {
				assert.Equal(t, [][]notificationtemplate.Button{{{
					Text: alert.ResourceID,
					URL:  "https://example.com/repositories?repo=example.com%2Forg%2F%3Crepo%3E%26test",
				}}}, req.Markup.Rows)
			}
			if custom {
				assert.Empty(t, received[0].ParseMode)
				assert.Empty(t, received[1].ParseMode)
				assert.Contains(t, received[0].Text, "<repo>&test")
				assert.Contains(t, received[1].Text, "<repo>&test")
			} else {
				for _, req := range received {
					assert.Equal(t, "HTML", req.ParseMode)
					assert.Contains(t, req.Text, "<code>example.com/org/&lt;repo&gt;&amp;test</code>")
				}
				assert.Contains(t, received[0].Text, "Git &lt;network&gt; &amp; unavailable")
			}
		})
	}
}
