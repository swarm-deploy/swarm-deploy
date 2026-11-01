package compose

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
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
				Keys: []string{"label"},
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

func TestEnvironmentNullBareAndEmptyPreparation(t *testing.T) {
	t.Setenv("ENV_AUDIT", "from-controller")
	for _, syntax := range []string{"[ENV_AUDIT]", "{ENV_AUDIT: null}"} {
		t.Run(syntax, func(t *testing.T) {
			loader := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
				return []byte("services: {api: {image: alpine, environment: " + syntax + "}}"), nil
			})
			file, err := loader.Load(t.Context(), "compose.yaml")
			require.NoError(t, err)
			assert.Equal(t, "from-controller", file.Compose.Services[0].Environment.Map["ENV_AUDIT"])
			rendered, err := file.MarshalYAML()
			require.NoError(t, err)
			t.Setenv("ENV_AUDIT", "changed")
			snapshot, err := Parse(rendered)
			require.NoError(t, err)
			assert.Equal(t, "from-controller", snapshot.Services[0].Environment.Map["ENV_AUDIT"])
		})
	}
	var env Environment
	require.NoError(t, yaml.Unmarshal([]byte("{ENV_AUDIT: '', NULL_VALUE: null, TEXT: 'null'}"), &env))
	env.resolve(func(key string) (string, bool) { return "hidden", key == "ENV_AUDIT" })
	assert.Empty(t, env.Map["ENV_AUDIT"], "explicit empty stays empty")
	assert.Equal(t, []string{"NULL_VALUE"}, env.Unset)
	assert.Equal(t, "null", env.Map["TEXT"])
	raw, err := yaml.Marshal(env)
	require.NoError(t, err)
	var again Environment
	require.NoError(t, yaml.Unmarshal(raw, &again))
	assert.Equal(t, env.Map, again.Map)
	assert.Equal(t, env.Unset, again.Unset)
}

func TestEnvironmentDuplicateExplicitValueOverridesBareKey(t *testing.T) {
	env, err := NewEnvironment([]string{"KEY", "KEY=explicit"})
	require.NoError(t, err)
	assert.Empty(t, env.Unset)
	assert.Equal(t, "explicit", env.Map["KEY"])
}
