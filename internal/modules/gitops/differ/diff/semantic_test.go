package diff

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceDiffCalcHasChangesForSemanticFields(t *testing.T) {
	testCases := []struct {
		name string
		diff ServiceDiff
	}{
		{name: "healthcheck", diff: ServiceDiff{Healthcheck: &HealthcheckDiff{Interval: &ScalarDiff[string]{}}}},
		{name: "deploy", diff: ServiceDiff{Deploy: &DeployDiff{Replicas: &ScalarDiff[uint64]{}}}},
		{name: "cap add", diff: ServiceDiff{CapAdd: &StringSetDiff{Added: []string{"NET_ADMIN"}}}},
		{name: "cap drop", diff: ServiceDiff{CapDrop: &StringSetDiff{Removed: []string{"ALL"}}}},
		{name: "labels", diff: ServiceDiff{Labels: []LabelDiff{{Key: "role", Added: true}}}},
		{name: "logging", diff: ServiceDiff{Logging: &LoggingDiff{Driver: &ScalarDiff[string]{}}}},
		{name: "env files", diff: ServiceDiff{EnvFiles: &StringListDiff{New: []string{"app.env"}}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := testCase.diff
			actual.CalcHasChanges()

			assert.True(t, actual.HasChanges, "semantic change must set HasChanges")
		})
	}
}

func TestDeployDiffJSONContainsOnlyChangedFields(t *testing.T) {
	value := ServiceDiff{Deploy: &DeployDiff{
		Replicas: &ScalarDiff[uint64]{Old: 2, New: 3},
		UpdateConfig: &UpdateConfigDiff{
			Order: &ScalarDiff[string]{Old: "stop-first", New: "start-first"},
		},
	}}

	encoded, err := json.Marshal(value)
	require.NoError(t, err, "marshal semantic deploy diff")
	assert.JSONEq(t, `{
		"serviceName":"",
		"stackName":"",
		"hasChanges":false,
		"deploy":{
			"replicas":{"old":2,"new":3},
			"updateConfig":{"order":{"old":"stop-first","new":"start-first"}}
		}
	}`, string(encoded), "unexpected semantic deploy JSON")
}
