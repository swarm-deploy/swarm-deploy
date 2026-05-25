package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestEnvironment_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		Title    string
		Input    string
		Expected Environment
	}{
		{
			Title: "sequence",
			Input: `
- label=value
`,
			Expected: Environment{
				Map: map[string]string{
					"label": "value",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			var env Environment

			err := yaml.Unmarshal([]byte(test.Input), &env)
			require.NoError(t, err)

			assert.Equal(t, test.Expected, env)
		})
	}
}
