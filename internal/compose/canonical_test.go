package compose

import (
	"encoding/json"
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
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			before := mustCanonicalCompose(t, testCase.before)
			after := mustCanonicalCompose(t, testCase.after)
			assert.Equal(t, before, after)
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

func TestCanonicalizePreservesSignificantListOrdering(t *testing.T) {
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
			name: "init jobs",
			before: `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - {name: migrate, image: alpine, command: [migrate]}
      - {name: seed, image: alpine, command: [seed]}
`,
			after: `services:
  api:
    image: alpine
    x-init-deploy-jobs:
      - {name: seed, image: alpine, command: [seed]}
      - {name: migrate, image: alpine, command: [migrate]}
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
			assert.NotEqual(t, mustCanonicalCompose(t, testCase.before), mustCanonicalCompose(t, testCase.after))
		})
	}
}

func mustCanonicalCompose(t *testing.T, raw string) []byte {
	t.Helper()
	parsed, err := Parse([]byte(raw))
	require.NoError(t, err)
	canonical, err := Canonicalize(*parsed)
	require.NoError(t, err)
	return canonical
}
