package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLabels_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		Title    string
		Input    string
		Expected Labels
	}{
		{
			Title: "map",
			Input: `{label1: value1, label2: ""}`,
			Expected: Labels{
				Map: map[string]string{
					"label1": "value1",
					"label2": "",
				},
				isMap: true,
			},
		},
		{
			Title: "sequence",
			Input: `
- label1=value1
- label2
`,
			Expected: Labels{
				Map: map[string]string{
					"label1": "value1",
					"label2": "",
				},
				isMap: false,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			var labels Labels

			err := yaml.Unmarshal([]byte(test.Input), &labels)
			require.NoError(t, err)

			assert.Equal(t, test.Expected, labels)
		})
	}
}
