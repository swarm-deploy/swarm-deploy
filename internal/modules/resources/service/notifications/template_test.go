package notifications

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
)

func TestPayloadContract(t *testing.T) {
	typ := reflect.TypeFor[Payload]()
	for name, expected := range map[string]reflect.Type{
		"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "StackName": reflect.TypeFor[string](),
		"ServiceName": reflect.TypeFor[string](), "Commit": reflect.TypeFor[string](),
		"Username": reflect.TypeFor[string](), "PreviousReplicas": reflect.TypeFor[uint64](),
		"CurrentReplicas": reflect.TypeFor[uint64](), "Message": reflect.TypeFor[string](),
		"OccurredAt": reflect.TypeFor[time.Time](),
	} {
		field, ok := typ.FieldByName(name)
		require.True(t, ok)
		assert.Equal(t, expected, field.Type)
	}
}

func TestDefaultTemplate(t *testing.T) {
	for _, eventType := range []string{"serviceMissed", "servicePruned", "serviceReplicasIncreased", "serviceReplicasDecreased", "serviceRestarted"} {
		t.Run(eventType, func(t *testing.T) {
			payload := ReferencePayload(eventType)
			renderer, err := notificationtemplate.New(DefaultTemplate())
			require.NoError(t, err)
			actual, err := renderer.Render(payload)
			require.NoError(t, err)
			assert.Equal(t, payload.Message, actual)
		})
	}
}
