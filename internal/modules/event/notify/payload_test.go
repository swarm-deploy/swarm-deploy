package notify

import (
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

func TestDeploymentNotificationPayload(t *testing.T) {
	occurredAt := time.Date(2026, time.October, 13, 12, 30, 0, 0, time.FixedZone("test", 3*60*60))
	payload, err := notificationPayload(events.Envelope{ID: "event-1", OccurredAt: occurredAt, Event: &events.DeployFailed{
		DeployEvent: events.DeployEvent{DeploymentID: "deployment-1", StackName: "payments", Commit: "abc123",
			Services: []compose.Service{{Name: "api", Image: "registry.example.com/api:v3"}}},
		Error: errors.New("apply rejected"), ErrorCode: "apply_failed", Stage: "apply",
	}})
	require.NoError(t, err)

	actual, ok := payload.(eventnotifications.DeploymentPayload)
	require.True(t, ok)
	assert.Equal(t, "event-1", actual.ID)
	assert.Equal(t, eventnotifications.StatusFailed, actual.Status)
	assert.Equal(t, "payments", actual.StackName)
	assert.Equal(t, []eventnotifications.DeploymentService{{Name: "api", Image: "registry.example.com/api:v3"}}, actual.Services)
	assert.Equal(t, "apply rejected", actual.Error)
	assert.Equal(t, occurredAt.UTC(), actual.OccurredAt)
}

func TestEveryConfigurableEventHasTemplateContract(t *testing.T) {
	eventTypes := []events.TypeName{events.TypeNameDeployFailed}
	for _, eventType := range events.Types {
		eventTypes = append(eventTypes, eventType.Name())
	}
	for _, eventType := range eventTypes {
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
