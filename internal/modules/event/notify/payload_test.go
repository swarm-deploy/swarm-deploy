package notify

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notifications"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
)

func TestDeploymentFailureNotificationPayloadsUseSyncFailedContract(t *testing.T) {
	occurredAt := time.Date(2026, time.October, 13, 12, 30, 0, 0, time.FixedZone("test", 3*60*60))
	services := []compose.Service{{
		Name: "api", Image: "registry.example.com/api:v3",
		Environment: compose.Environment{Map: map[string]string{"TOKEN": "private"}},
	}}
	tests := []struct {
		name         string
		event        events.Event
		status       string
		deploymentID string
		errorCode    string
	}{
		{name: "deployment failure", status: eventnotifications.StatusFailed, deploymentID: "deployment-1",
			errorCode: "apply_failed", event: &events.DeployFailed{
				DeployEvent: events.DeployEvent{DeploymentID: "deployment-1", StackName: "payments", Commit: "abc123",
					Services: services},
				Error: errors.New("token=private apply rejected"), ErrorCode: "apply_failed", Stage: "apply",
			}},
		{name: "preparation failure", status: eventnotifications.StatusPreparationFailed,
			errorCode: "preparation_failed", event: &events.DeployPreparationFailed{
				StackName: "payments", Commit: "abc123", Services: services,
				Error: errors.New("token=private validation failed"), ErrorCode: "preparation_failed", Stage: "validation",
			}},
		{name: "interrupted deployment", status: eventnotifications.StatusInterrupted, deploymentID: "deployment-2",
			errorCode: "process_interrupted", event: &events.DeployInterrupted{
				DeploymentID: "deployment-2", StackName: "payments", Commit: "abc123", Services: services,
				Error: errors.New("token=private process stopped"), Reason: "process_interrupted", Stage: "apply",
			}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			payload, err := notificationPayload(events.Envelope{
				ID: "event-1", OccurredAt: occurredAt, Event: testCase.event,
			})
			require.NoError(t, err)
			actual, ok := payload.(eventnotifications.DeploymentPayload)
			require.True(t, ok)
			assert.Equal(t, "event-1", actual.ID)
			assert.Equal(t, testCase.status, actual.Status)
			assert.Equal(t, string(events.TypeNameSyncFailed), actual.Type)
			assert.Equal(t, testCase.deploymentID, actual.DeploymentID)
			assert.Equal(t, testCase.errorCode, actual.ErrorCode)
			assert.Equal(t, "payments", actual.StackName)
			assert.Equal(t, []eventnotifications.DeploymentService{{Name: "api", Image: "registry.example.com/api:v3"}}, actual.Services)
			assert.Contains(t, actual.Error, "token=****")
			assert.NotContains(t, actual.Error, "private")
			assert.Equal(t, occurredAt.UTC(), actual.OccurredAt)

			template, ok := StandardTemplate(events.TypeNameSyncFailed)
			require.True(t, ok)
			require.NoError(t, notificationtemplate.Validate(template, actual))

			encoded, err := json.Marshal(actual)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private")
		})
	}
}

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
