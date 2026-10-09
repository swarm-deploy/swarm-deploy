package legacyimport

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLegacyWrapperValidation(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		valid         bool
	}{
		{"alerts.state.json", `{"alerts":[]}`, true},
		{"alerts.state.json", `{"alert":[]}`, false},
		{"secrets.state.json", `{}`, false},
		{"recommendations.state.json", `{"list":[]}`, true},
		{"index.json", `{"chats":[]}`, true},
		{"index.json", `{}`, false},
		{"controller.state.json", `null`, false},
		{"controller.state.json", `{"stacks":{}}`, true},
	} {
		t.Run(tc.name+tc.content, func(t *testing.T) {
			err := validateJSONShape(tc.name, []byte(tc.content))
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
