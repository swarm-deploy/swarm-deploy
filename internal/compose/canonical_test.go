package compose

import (
	"encoding/json/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalizeEquivalentCompose(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			name: "service and reference ordering",
			before: `services:
  worker: {image: alpine}
  api: {image: alpine, secrets: [one, two], configs: [app]}
`,
			after: `services:
  api:
    image: alpine
    secrets: [{source: two, target: /run/secrets/two}, {source: one, target: one}]
    configs: [{source: app, target: /app, uid: "0", gid: "0", mode: 0444}]
  worker: {image: alpine}
`,
		},
		{
			name: "execution syntax and capabilities",
			before: `services:
  api:
    image: alpine
    command: sleep 100
    entrypoint: /bin/sh -c 'echo hello'
    healthcheck: {test: echo ready}
    cap_add: [net_admin, CAP_NET_ADMIN, RESET]
    cap_drop: [sys_admin, net_admin]
`,
			after: `services:
  api:
    image: alpine
    command: [sleep, "100"]
    entrypoint: [/bin/sh, -c, echo hello]
    healthcheck: {test: [CMD-SHELL, echo ready]}
    cap_add: [CAP_NET_ADMIN]
    cap_drop: [CAP_SYS_ADMIN]
`,
		},
		{
			name: "init job secret targets",
			before: `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - name: prepare
        image: alpine
        secrets: [token, {source: key}, {source: cert, target: tls.crt}]
`,
			after: `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - name: prepare
        image: alpine
        secrets:
          - {source: token, target: /run/secrets/token}
          - {source: key, target: /run/secrets/key, uid: "0", gid: "0"}
          - {source: cert, target: /run/secrets/tls.crt}
`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			before := mustCanonicalCompose(t, testCase.before)
			after := mustCanonicalCompose(t, testCase.after)
			assert.Equal(t, before, after)
		})
	}
}

func TestCanonicalizeNormalizesProgrammaticCapabilities(t *testing.T) {
	parsed, err := Parse([]byte(`services:
  api: {image: alpine, cap_add: [NET_ADMIN], cap_drop: [SYS_ADMIN]}
`))
	require.NoError(t, err)
	programmatic := *parsed
	programmatic.Services = slices.Clone(parsed.Services)
	programmatic.Services[0].CapAdd = Capabilities{"net_admin", "CAP_NET_ADMIN", "RESET"}
	programmatic.Services[0].CapDrop = Capabilities{"sys_admin", "net_admin"}

	expected, err := Canonicalize(*parsed)
	require.NoError(t, err)
	actual, err := Canonicalize(programmatic)
	require.NoError(t, err)

	assert.Equal(t, expected, actual)
	assert.Equal(t, Capabilities{"net_admin", "CAP_NET_ADMIN", "RESET"}, programmatic.Services[0].CapAdd)
	assert.Equal(t, Capabilities{"sys_admin", "net_admin"}, programmatic.Services[0].CapDrop)
}

// TestCanonicalizeInitJobObjectReferences pins the canonical form of Init Job references.
// It follows InitJobRunner: a config without a target is attached without a file, and
// a short-form config keeps the secret-style target assigned by the parser.
func TestCanonicalizeInitJobObjectReferences(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		refs     string
		expected []any
	}{
		{
			name:     "config without target",
			field:    ConfigsField,
			refs:     `[{source: app, uid: "1000", gid: "1000"}]`,
			expected: []any{map[string]any{"source": "app"}},
		},
		{
			name:     "config with target and default ownership",
			field:    ConfigsField,
			refs:     `[{source: app, target: /app, uid: "0", gid: "0"}]`,
			expected: []any{map[string]any{"source": "app", "target": "/app"}},
		},
		{
			name:     "short-form config",
			field:    ConfigsField,
			refs:     `[app]`,
			expected: []any{map[string]any{"source": "app", "target": "/run/secrets/app"}},
		},
		{
			name:  "secrets",
			field: SecretsField,
			refs:  `[token, {source: cert, target: tls.crt, uid: "1000"}]`,
			expected: []any{
				map[string]any{"source": "cert", "target": "/run/secrets/tls.crt", "uid": "1000"},
				map[string]any{"source": "token", "target": "/run/secrets/token"},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			canonical := mustCanonicalCompose(t, `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - name: prepare
        image: alpine
        `+testCase.field+`: `+testCase.refs+`
`)
			var tree map[string]any
			require.NoError(t, json.Unmarshal(canonical, &tree))
			service := tree["services"].([]any)[0].(map[string]any)
			job := service["init_jobs"].([]any)[0].(map[string]any)
			assert.Equal(t, testCase.expected, job[testCase.field])
		})
	}
}

func TestCanonicalizePreservesUserMapEntries(t *testing.T) {
	canonical := mustCanonicalCompose(t, `services:
  api:
    image: alpine
    environment:
      Keys: value-keys
      env_file: value-env-file
    labels:
      Map: value-map
      Keys: value-keys
      env_file: value-env-file
      Volumes: value-volumes
      List: value-list
      AliasMap: value-alias-map
      Names: value-names
      Aliases: value-aliases
`)
	var tree map[string]any
	require.NoError(t, json.Unmarshal(canonical, &tree))
	services := tree["services"].([]any)
	service := services[0].(map[string]any)
	environment := service["environment"].(map[string]any)["Map"].(map[string]any)
	assert.Equal(t, "value-keys", environment["Keys"])
	assert.Equal(t, "value-env-file", environment["env_file"])
	labels := service["labels"].(map[string]any)["Map"].(map[string]any)
	assert.Equal(t, map[string]any{
		"AliasMap": "value-alias-map", "Aliases": "value-aliases", "Keys": "value-keys",
		"List": "value-list", "Map": "value-map", "Names": "value-names",
		"Volumes": "value-volumes", "env_file": "value-env-file",
	}, labels)
}

func TestCanonicalizeIgnoresInsignificantListOrdering(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			name: "mounts",
			before: `services: {api: {image: alpine, volumes: [data:/data, cache:/cache]}}
volumes: {data: {}, cache: {}}
`,
			after: `services: {api: {image: alpine, volumes: [cache:/cache, data:/data]}}
volumes: {data: {}, cache: {}}
`,
		},
		{
			name: "init job object references",
			before: `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - name: prepare
        image: alpine
        configs: [{source: first}, {source: second}]
`,
			after: `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - name: prepare
        image: alpine
        configs: [{source: second}, {source: first}]
`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, mustCanonicalCompose(t, testCase.before), mustCanonicalCompose(t, testCase.after))
		})
	}
}

func TestCanonicalizePreservesInitJobOrdering(t *testing.T) {
	before := `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - {name: migrate, image: alpine, command: [migrate]}
      - {name: seed, image: alpine, command: [seed]}
`
	after := `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - {name: seed, image: alpine, command: [seed]}
      - {name: migrate, image: alpine, command: [migrate]}
`

	assert.NotEqual(t, mustCanonicalCompose(t, before), mustCanonicalCompose(t, after))
}

func mustCanonicalCompose(t *testing.T, raw string) []byte {
	t.Helper()
	parsed, err := Parse([]byte(raw))
	require.NoError(t, err)
	canonical, err := Canonicalize(*parsed)
	require.NoError(t, err)
	return canonical
}
