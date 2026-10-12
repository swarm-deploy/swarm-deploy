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

func TestPayloadContract(t *testing.T) {
	typ := reflect.TypeFor[Payload]()
	expected := map[string]reflect.Type{
		"ID": reflect.TypeFor[string](), "DeploymentID": reflect.TypeFor[string](),
		"Type": reflect.TypeFor[string](), "Status": reflect.TypeFor[string](),
		"StackName": reflect.TypeFor[string](), "Services": reflect.TypeFor[[]Service](),
		"Commit": reflect.TypeFor[string](), "Error": reflect.TypeFor[string](),
		"ErrorCode": reflect.TypeFor[string](), "Stage": reflect.TypeFor[string](),
		"OccurredAt": reflect.TypeFor[time.Time](),
	}
	for name, expectedType := range expected {
		field, ok := typ.FieldByName(name)
		require.True(t, ok, "expected public field %s", name)
		assert.Equal(t, expectedType, field.Type, "unexpected type of %s", name)
	}
}

func TestStandardTemplates(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		payload Payload
		golden  string
	}{
		{name: "success", source: SuccessTemplate(), payload: SuccessReferencePayload(), golden: "success.golden"},
		{name: "failed", source: FailedTemplate(), payload: FailureReferencePayloads()[0], golden: "failed.golden"},
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
	for _, payload := range FailureReferencePayloads() {
		require.NoError(t, notificationtemplate.Validate(FailedTemplate(), payload))
	}
}
