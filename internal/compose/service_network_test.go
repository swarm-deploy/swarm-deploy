package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func Test_ServiceNetwork_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		Title    string
		Input    string
		Expected *ServiceNetworks
	}{
		{
			Title: "parse string value, only alias",
			Input: "- infra",
			Expected: &ServiceNetworks{
				Names:   []string{"infra"},
				Aliases: []string{"infra"},
				AliasMap: map[string]*ServiceNetwork{
					"infra": {
						Alias: "infra",
					},
				},
				List: []*ServiceNetwork{
					{
						Alias: "infra",
					},
				},
				onlyAlias: true,
			},
		},
		{
			Title: "parse map",
			Input: `
infra:
  aliases:
    - my-app
  x-key: val
`,
			Expected: &ServiceNetworks{
				Names:   []string{"infra"},
				Aliases: []string{"infra"},
				AliasMap: map[string]*ServiceNetwork{
					"infra": {
						Alias:   "infra",
						Aliases: []string{"my-app"},
						Extra: map[string]interface{}{
							"x-key": "val",
						},
					},
				},
				List: []*ServiceNetwork{
					{
						Alias:   "infra",
						Aliases: []string{"my-app"},
						Extra: map[string]interface{}{
							"x-key": "val",
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			sn := ServiceNetworks{}

			err := yaml.Unmarshal([]byte(test.Input), &sn)
			require.NoError(t, err)

			assert.Equal(t, test.Expected, &sn)
		})
	}
}
