package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

func TestDeployFailedNotificationCompatibility(t *testing.T) {
	assert.Equal(t, []events.TypeName{
		events.TypeNameDeployFailed,
		events.TypeNameDeployPreparationFailed,
		events.TypeNameDeployInterrupted,
	}, notificationEventTypes(events.TypeNameDeployFailed))
}
