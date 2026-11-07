package modelstore

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerificationReadsHistoricalPayloadsAndPreservesKnownEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		known         bool
		names         []string
	}{
		{name: "legacy absent", payload: `{}`},
		{name: "unknown", payload: `{"unresolved_services":null}`},
		{name: "known empty", payload: `{"unresolved_services":[]}`, known: true},
		{name: "known unresolved", payload: `{"unresolved_services":["api"]}`, known: true, names: []string{"api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := deploymentRow{DetailsPayload: []byte(tc.payload), ChangesPayload: []byte(`[]`)}
			deployment, err := row.deployment()
			require.NoError(t, err)
			assert.Equal(t, tc.known, deployment.Verification.Known)
			assert.ElementsMatch(t, tc.names, deployment.Verification.UnresolvedServices)
			// Allocation of an empty slice has no domain meaning.
			if len(tc.names) == 0 {
				deployment.Verification.UnresolvedServices = nil
			}
			details, changes, err := marshalDeploymentPayloads(deployment)
			require.NoError(t, err)
			decoded, err := (deploymentRow{DetailsPayload: []byte(details), ChangesPayload: []byte(changes)}).deployment()
			require.NoError(t, err)
			assert.Equal(t, tc.known, decoded.Verification.Known)
			assert.ElementsMatch(t, tc.names, decoded.Verification.UnresolvedServices)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(details), &fields))
			if tc.known && len(tc.names) == 0 {
				assert.JSONEq(t, `[]`, string(fields["unresolved_services"]))
			}
		})
	}
}
