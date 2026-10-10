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

func TestDeploymentLifecycleFactsAreNotPublicEventTypes(t *testing.T) {
	for _, typ := range []events.TypeName{
		events.TypeNameDeployFailed,
		events.TypeNameDeployPreparationFailed,
		events.TypeNameDeployInterrupted,
	} {
		t.Run(string(typ), func(t *testing.T) {
			assert.False(t, typ.Valid())
			_, public := events.ParseType(string(typ))
			assert.False(t, public)
			for _, registered := range events.Types {
				assert.NotEqual(t, typ, registered.Name())
			}
		})
	}
}
