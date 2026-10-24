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
		"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "EventType": reflect.TypeFor[string](),
		"Destination": reflect.TypeFor[string](), "Channel": reflect.TypeFor[string](),
		"DestinationKey": reflect.TypeFor[string](),
		"Error":          reflect.TypeFor[string](), "Message": reflect.TypeFor[string](),
		"OccurredAt": reflect.TypeFor[time.Time](),
	} {
		field, ok := typ.FieldByName(name)
		require.True(t, ok)
		assert.Equal(t, expected, field.Type)
	}
}

func TestDefaultTemplate(t *testing.T) {
	renderer, err := notificationtemplate.New(DefaultTemplate())
	require.NoError(t, err)
	actual, err := renderer.Render(ReferencePayload())
	require.NoError(t, err)
	assert.Equal(t, "Failed to send notification", actual)
}
