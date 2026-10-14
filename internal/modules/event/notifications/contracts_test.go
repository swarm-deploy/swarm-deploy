package notifications

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
)

func TestMovedPayloadContracts(t *testing.T) {
	tests := []struct {
		name   string
		typ    reflect.Type
		fields map[string]reflect.Type
	}{
		{name: "deployment", typ: reflect.TypeFor[DeploymentPayload](), fields: map[string]reflect.Type{
			"ID": reflect.TypeFor[string](), "DeploymentID": reflect.TypeFor[string](),
			"Type": reflect.TypeFor[string](), "Status": reflect.TypeFor[string](),
			"StackName": reflect.TypeFor[string](), "Services": reflect.TypeFor[[]DeploymentService](),
			"Commit": reflect.TypeFor[string](), "OccurredAt": reflect.TypeFor[time.Time](),
		}},
		{name: "node", typ: reflect.TypeFor[NodePayload](), fields: map[string]reflect.Type{
			"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "NodeID": reflect.TypeFor[string](),
			"NodeName": reflect.TypeFor[string](), "Role": reflect.TypeFor[string](), "Status": reflect.TypeFor[string](),
			"Message": reflect.TypeFor[string](), "OccurredAt": reflect.TypeFor[time.Time](),
		}},
		{name: "service", typ: reflect.TypeFor[ServicePayload](), fields: map[string]reflect.Type{
			"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "StackName": reflect.TypeFor[string](),
			"ServiceName": reflect.TypeFor[string](), "Commit": reflect.TypeFor[string](),
			"Username": reflect.TypeFor[string](), "PreviousReplicas": reflect.TypeFor[uint64](),
			"CurrentReplicas": reflect.TypeFor[uint64](), "Message": reflect.TypeFor[string](),
			"OccurredAt": reflect.TypeFor[time.Time](),
		}},
		{name: "reconciliation", typ: reflect.TypeFor[ReconciliationPayload](), fields: map[string]reflect.Type{
			"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "StackName": reflect.TypeFor[string](),
			"Username": reflect.TypeFor[string](), "Queued": reflect.TypeFor[bool](),
			"NetworkName": reflect.TypeFor[string](), "NetworkID": reflect.TypeFor[string](),
			"Driver": reflect.TypeFor[string](), "Message": reflect.TypeFor[string](),
			"OccurredAt": reflect.TypeFor[time.Time](),
		}},
		{name: "security", typ: reflect.TypeFor[SecurityPayload](), fields: map[string]reflect.Type{
			"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "Username": reflect.TypeFor[string](),
			"Message": reflect.TypeFor[string](), "OccurredAt": reflect.TypeFor[time.Time](),
		}},
		{name: "assistant", typ: reflect.TypeFor[AssistantPayload](), fields: map[string]reflect.Type{
			"ID": reflect.TypeFor[string](), "Type": reflect.TypeFor[string](), "Detector": reflect.TypeFor[string](),
			"Username": reflect.TypeFor[string](), "Message": reflect.TypeFor[string](),
			"OccurredAt": reflect.TypeFor[time.Time](),
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for name, expected := range test.fields {
				field, ok := test.typ.FieldByName(name)
				require.True(t, ok, "expected public field %s", name)
				assert.Equal(t, expected, field.Type, "unexpected type of %s", name)
			}
		})
	}
}

func TestMessagePayloadsUseDefaultTemplate(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		want    string
	}{
		{name: "node joined", payload: NodeJoinedReferencePayload(), want: "Node worker-1 joined the swarm"},
		{name: "node connected", payload: NodeConnectedReferencePayload(), want: "Node worker-1 connected"},
		{name: "node disconnected", payload: NodeDisconnectedReferencePayload(), want: "Node worker-1 disconnected"},
		{name: "service missed", payload: ServiceReferencePayload("ServiceMissed"),
			want: "Service payments/api missed"},
		{name: "service pruned", payload: ServiceReferencePayload("ServicePruned"),
			want: "Service payments/api pruned"},
		{name: "service replicas increased", payload: ServiceReferencePayload("ServiceReplicasIncreased"),
			want: "Service payments/api replicas increased from 2 to 3"},
		{name: "service replicas decreased", payload: ServiceReferencePayload("ServiceReplicasDecreased"),
			want: "Service payments/api replicas decreased from 3 to 2"},
		{name: "service restarted", payload: ServiceReferencePayload("ServiceRestarted"),
			want: "Service payments/api restarted"},
		{name: "service catalog updated", payload: ReconciliationReferencePayload("ServiceCatalogUpdated"),
			want: "Service catalog updated"},
		{name: "manual sync started", payload: ReconciliationReferencePayload("SyncManualStarted"),
			want: "Manual sync started"},
		{name: "webhook received", payload: ReconciliationReferencePayload("WebhookReceived"),
			want: "Webhook received"},
		{name: "network created", payload: ReconciliationReferencePayload("NetworkCreated"),
			want: "Network payments created"},
		{name: "security", payload: SecurityReferencePayload(), want: "User operator authenticated"},
		{name: "assistant", payload: AssistantReferencePayload(), want: "Detected prompt injection"},
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

func TestDeploymentSuccessTemplate(t *testing.T) {
	renderer, err := notificationtemplate.New(SuccessTemplate())
	require.NoError(t, err)
	actual, err := renderer.Render(DeploymentSuccessReferencePayload())
	require.NoError(t, err)
	expected, err := os.ReadFile(filepath.Join("testdata", "success.golden"))
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSuffix(string(expected), "\n"), actual)
}

func TestNotificationPayloadJSONUsesStandardAttributeNames(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		keys    []string
	}{
		{name: "deployment", payload: DeploymentSuccessReferencePayload(), keys: []string{
			events.AttributeDeploymentID, events.AttributeStackName, events.AttributeCommit, events.AttributeStatus,
		}},
		{name: "node", payload: NodeConnectedReferencePayload(), keys: []string{
			events.AttributeNodeID, events.AttributeNodeName, events.AttributeStatus,
		}},
		{name: "service", payload: ServiceReferencePayload("ServiceReplicasIncreased"), keys: []string{
			events.AttributeStackName, events.AttributeServiceName, events.AttributeCommit, events.AttributeUsername,
		}},
		{name: "reconciliation", payload: ReconciliationReferencePayload("SyncManualStarted"), keys: []string{
			events.AttributeStackName, events.AttributeUsername, events.AttributeNetworkID, events.AttributeNetworkName,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.payload)
			require.NoError(t, err)

			var value map[string]any
			require.NoError(t, json.Unmarshal(encoded, &value))
			for _, key := range test.keys {
				assert.Contains(t, value, key)
			}
			assert.NotContains(t, value, "stack")
			assert.NotContains(t, value, "service")
			assert.NotContains(t, value, "triggered_by")
		})
	}
}

func TestManualSyncTemplateUsesUsername(t *testing.T) {
	payload := ReconciliationReferencePayload("SyncManualStarted")
	require.NoError(t, notificationtemplate.Validate(`{{ .Username }}`, payload))
	assert.ErrorContains(t, notificationtemplate.Validate(`{{ .TriggeredBy }}`, payload), "TriggeredBy")
}
