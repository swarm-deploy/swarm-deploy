package notify

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
)

func TestAssistantNotificationPayloadOmitsPrompt(t *testing.T) {
	payload, err := notificationPayload(events.Envelope{
		ID: "event-1",
		Event: &events.AssistantPromptInjectionDetected{
			Prompt: "token=private", Detector: events.AssistantPromptInjectionDetectorRegexp, Username: "operator",
		},
	})
	require.NoError(t, err)

	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private")

	var value map[string]any
	require.NoError(t, json.Unmarshal(encoded, &value))
	assert.NotContains(t, value, "prompt")
}

func TestEveryConfigurableEventHasTemplateContract(t *testing.T) {
	for _, eventType := range events.Types {
		if !eventType.IsPublic() {
			continue
		}
		eventType := eventType.Name()
		t.Run(string(eventType), func(t *testing.T) {
			source, ok := StandardTemplate(eventType)
			require.True(t, ok)
			payloads, ok := ReferencePayloads(eventType)
			require.True(t, ok)
			require.NotEmpty(t, payloads)
			for _, payload := range payloads {
				require.NoError(t, notificationtemplate.Validate(source, payload))
			}
		})
	}
}

func TestServiceRemovedFromDesiredHasNoDirectNotificationContract(t *testing.T) {
	assert.False(t, events.TypeNameServiceRemovedFromDesired.Valid())
	_, standard := StandardTemplate(events.TypeNameServiceRemovedFromDesired)
	assert.False(t, standard)
	_, reference := ReferencePayloads(events.TypeNameServiceRemovedFromDesired)
	assert.False(t, reference)
}
