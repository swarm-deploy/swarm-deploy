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
		"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "NodeID": reflect.TypeFor[string](),
		"NodeName": reflect.TypeFor[string](), "Role": reflect.TypeFor[string](), "Status": reflect.TypeFor[string](),
		"Message": reflect.TypeFor[string](), "OccurredAt": reflect.TypeFor[time.Time](),
	} {
		field, ok := typ.FieldByName(name)
		require.True(t, ok)
		assert.Equal(t, expected, field.Type)
	}
}

func TestDefaultTemplate(t *testing.T) {
	tests := []struct {
		name    string
		payload Payload
		want    string
	}{
		{name: "joined", payload: JoinedReferencePayload(), want: "Node worker-1 joined the swarm"},
		{name: "connected", payload: ConnectedReferencePayload(), want: "Node worker-1 connected"},
		{name: "disconnected", payload: DisconnectedReferencePayload(), want: "Node worker-1 disconnected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			renderer, err := notificationtemplate.New(DefaultTemplate())
			require.NoError(t, err)
			actual, err := renderer.Render(test.payload)
			require.NoError(t, err)
			assert.Equal(t, test.want, actual)
		})
	}
}
