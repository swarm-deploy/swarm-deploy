package notifiers

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/actions"
)

func TestCustomWebhookSendsActions(t *testing.T) {
	type request struct {
		Payload map[string]string `json:"Payload"`
		Actions []actions.Action  `json:"actions"`
	}
	received := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body request
		require.NoError(t, json.UnmarshalRead(r.Body, &body))
		received <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	notifier := newCustomWebhookNotifier("pwa", server.URL, http.MethodPost, nil)
	payload := map[string]string{"deployment_id": "deployment-1"}
	message := Message{
		Payload: payload,
		Actions: []actions.Action{{Text: "Open deployment", URL: "https://swarm.example/deployments?deployment_id=deployment-1"}},
	}
	_, err := notifier.Notify(t.Context(), message)
	require.NoError(t, err)

	actual := <-received
	assert.Equal(t, request{Payload: payload, Actions: message.Actions}, actual)
}
