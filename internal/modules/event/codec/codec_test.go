package codec

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

func TestSensitiveFieldsAreNeverPersisted(t *testing.T) {
	const secret = "super-private-token"
	service := compose.Service{Name: "api", Image: "nginx:stable",
		Environment: compose.Environment{Map: map[string]string{"TOKEN": secret}},
		Extra:       map[string]any{"password": secret},
	}
	for _, event := range []events.Event{
		&events.DeploySuccess{DeployEvent: events.DeployEvent{Services: []compose.Service{service}}},
		&events.DeployFailed{DeployEvent: events.DeployEvent{StackName: "stack", Commit: "revision", StackDefinition: compose.File{Path: secret}}, Error: errors.New(secret), Logs: []string{secret}},
		&events.AssistantPromptInjectionDetected{Prompt: secret},
		&events.SendNotificationFailed{Error: errors.New(secret)},
	} {
		t.Run(event.Type().String(), func(t *testing.T) {
			payload, err := Encode(event)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), secret)
			assert.NotContains(t, string(payload), "StackDefinition")
			assert.NotContains(t, string(payload), "Environment")
			decoded, err := Decode(event.Type().Name(), Version, payload)
			require.NoError(t, err)
			for _, value := range decoded.Details() {
				assert.NotContains(t, value, secret)
			}
		})
	}
}

func TestVersionAndPayloadValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     events.TypeName
		version int
		payload string
	}{
		{"future version", events.TypeNameNodeJoined, 2, `{}`},
		{"unknown type", "unknown", 1, `{}`},
		{"unknown field", events.TypeNameNodeJoined, 1, `{"password":"secret"}`},
		{"trailing data", events.TypeNameNodeJoined, 1, `{} {}`},
		{"null payload", events.TypeNameNodeJoined, 1, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) { _, err := Decode(tc.typ, tc.version, []byte(tc.payload)); require.Error(t, err) })
	}
}
