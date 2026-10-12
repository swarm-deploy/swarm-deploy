package notifications

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			"Commit": reflect.TypeFor[string](), "Error": reflect.TypeFor[string](),
			"ErrorCode": reflect.TypeFor[string](), "Stage": reflect.TypeFor[string](),
			"OccurredAt": reflect.TypeFor[time.Time](),
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
			"TriggeredBy": reflect.TypeFor[string](), "Queued": reflect.TypeFor[bool](),
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
		{name: "service missed", payload: ServiceReferencePayload("serviceMissed"),
			want: "Service payments/api missed"},
		{name: "service pruned", payload: ServiceReferencePayload("servicePruned"),
			want: "Service payments/api pruned"},
		{name: "service replicas increased", payload: ServiceReferencePayload("serviceReplicasIncreased"),
			want: "Service payments/api replicas increased from 2 to 3"},
		{name: "service replicas decreased", payload: ServiceReferencePayload("serviceReplicasDecreased"),
			want: "Service payments/api replicas decreased from 3 to 2"},
		{name: "service restarted", payload: ServiceReferencePayload("serviceRestarted"),
			want: "Service payments/api restarted"},
		{name: "service catalog updated", payload: ReconciliationReferencePayload("serviceCatalogUpdated"),
			want: "Service catalog updated"},
		{name: "manual sync started", payload: ReconciliationReferencePayload("syncManualStarted"),
			want: "Manual sync started"},
		{name: "webhook received", payload: ReconciliationReferencePayload("webhookReceived"),
			want: "Webhook received"},
		{name: "network created", payload: ReconciliationReferencePayload("networkCreated"),
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

func TestDeploymentTemplates(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		payload DeploymentPayload
		golden  string
	}{
		{name: "success", source: SuccessTemplate(), payload: DeploymentSuccessReferencePayload(),
			golden: "success.golden"},
		{name: "failed", source: FailedTemplate(), payload: DeploymentFailureReferencePayloads()[0],
			golden: "failed.golden"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			renderer, err := notificationtemplate.New(test.source)
			require.NoError(t, err)
			actual, err := renderer.Render(test.payload)
			require.NoError(t, err)
			expected, err := os.ReadFile(filepath.Join("testdata", test.golden))
			require.NoError(t, err)
			assert.Equal(t, strings.TrimSuffix(string(expected), "\n"), actual)
		})
	}
}

func TestFailureTemplateValidatesEveryFailureState(t *testing.T) {
	for _, payload := range DeploymentFailureReferencePayloads() {
		require.NoError(t, notificationtemplate.Validate(FailedTemplate(), payload))
	}
}
