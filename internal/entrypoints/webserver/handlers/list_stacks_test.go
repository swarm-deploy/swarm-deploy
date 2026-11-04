package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
)

func TestListStacksPollStatusModesAndOptionalFields(t *testing.T) {
	t.Parallel()
	pollAt := time.Date(2026, time.November, 4, 13, 57, 43, 0, time.UTC)
	testCases := []struct {
		name         string
		mode         string
		pollAt       time.Time
		wantPoll     bool
		wantPollData bool
	}{
		{name: "pull with data", mode: config.SyncModePull, pollAt: pollAt, wantPoll: true, wantPollData: true},
		{name: "hybrid without data", mode: config.SyncModeHybrid, wantPoll: true},
		{name: "webhook hides poll", mode: config.SyncModeWebhook},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{Sync: config.SyncSpec{Mode: testCase.mode}}}}
			state := modelstore.NewMemoryStore()
			require.NoError(t, state.Update(t.Context(), func(runtime *model.Runtime) {
				runtime.LastPollAt = testCase.pollAt
				runtime.LastPollResult = "no_change"
				runtime.LastPollError = "  sanitized diagnostic  "
				runtime.GitRevision = "6b220e1234567890abcdef1234567890abcdef12"
			}))
			response, err := (&handler{
				stackProvider: cfg, stateStore: state, history: newHistoryStore(t),
			}).ListStacks(t.Context())
			require.NoError(t, err)
			poll, present := response.Sync.LastPoll.Get()
			assert.Equal(t, testCase.wantPoll, present)
			if testCase.wantPoll {
				assert.Equal(t, testCase.wantPollData, poll.At.IsSet())
				assert.Equal(t, testCase.wantPollData, poll.Result.IsSet())
				assert.Equal(t, testCase.wantPollData, poll.Error.IsSet())
			}
			assert.Equal(t, "6b220e1234567890abcdef1234567890abcdef12", response.Sync.LastRevision.Value)

			payload, marshalErr := json.Marshal(response)
			require.NoError(t, marshalErr)
			for _, legacy := range []string{
				"last_sync_at", "last_sync_reason", "last_sync_result", "last_sync_error",
				"git_revision", "last_poll_at", "last_poll_result", "last_poll_error",
			} {
				assert.NotContains(t, string(payload), legacy)
			}
			if testCase.wantPoll && !testCase.wantPollData {
				assert.Contains(t, string(payload), `"last_poll":{}`)
			}
		})
	}
}

func TestListStacksWebhookStatusUsesLatestRetainedEvent(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		enabled      bool
		queuedValues []bool
		wantPresent  bool
		wantData     bool
		wantQueued   bool
	}{
		{name: "disabled", queuedValues: []bool{true}},
		{name: "enabled without history", enabled: true, wantPresent: true},
		{name: "enabled latest queued true", enabled: true, queuedValues: []bool{false, true}, wantPresent: true, wantData: true, wantQueued: true},
		{name: "enabled latest queued false", enabled: true, queuedValues: []bool{true, false}, wantPresent: true, wantData: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{Sync: config.SyncSpec{
				Mode: config.SyncModeWebhook, Webhook: config.WebhookSpec{Enabled: testCase.enabled},
			}}}}
			store := newHistoryStore(t)
			baseTime := time.Date(2026, time.November, 4, 13, 42, 7, 0, time.UTC)
			ids := []string{
				"c0e8a2f1-1234-4567-890a-123456789abc",
				"d1f9b3a2-2345-4678-9012-23456789abcd",
			}
			for index, queued := range testCase.queuedValues {
				require.NoError(t, store.Handle(t.Context(), events.Envelope{
					ID: ids[index], OccurredAt: baseTime.Add(time.Duration(index) * time.Minute),
					PublicationSequence: int64(index + 1), Event: &events.WebhookReceived{Queued: queued},
				}))
			}
			response, err := (&handler{
				stackProvider: cfg, stateStore: modelstore.NewMemoryStore(), history: store,
			}).ListStacks(t.Context())
			require.NoError(t, err)
			webhook, present := response.Sync.LastWebhook.Get()
			assert.Equal(t, testCase.wantPresent, present)
			if !testCase.wantPresent {
				return
			}
			assert.Equal(t, testCase.wantData, webhook.EventID.IsSet())
			assert.Equal(t, testCase.wantData, webhook.At.IsSet())
			assert.Equal(t, testCase.wantData, webhook.Queued.IsSet())
			if testCase.wantData {
				assert.Equal(t, ids[len(testCase.queuedValues)-1], webhook.EventID.Value)
				assert.Equal(t, testCase.wantQueued, webhook.Queued.Value)
			}
		})
	}
}
