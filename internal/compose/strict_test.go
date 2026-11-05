package compose

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictComposeRejectsUnsupportedFields(t *testing.T) {
	for _, tc := range []struct{ name, content, field string }{
		{"root", "include: other.yaml", "include"},
		{"service", "services: {api: {image: alpine, build: .}}", "build"},
		{"merged field", "services: {api: {deploy: {}, <<: {deploy: {unknown: true}}}}", "unknown"},
		{"nested deploy", "services: {api: {image: alpine, deploy: {resources: {limits: {devices: []}}}}}", "devices"},
		{"port", "services: {api: {image: alpine, ports: [{target: 80, unknown: true}]}}", "unknown"},
		{"mount", "services: {api: {image: alpine, volumes: [{type: bind, source: /tmp, target: /mnt, unknown: true}]}}", "unknown"},
		{"network", "networks: {default: {unknown: true}}", "unknown"},
		{"config", "configs: {cfg: {unknown: true}}", "unknown"},
		{"secret", "secrets: {secret: {unknown: true}}", "unknown"},
		{"volume", "volumes: {data: {unknown: true}}", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.content))
			require.ErrorContains(t, err, tc.field)
		})
	}
}

func TestStrictComposeToleratesIgnoredAndExtensionFields(t *testing.T) {
	source := []byte(`version: "3.8"
x-meta: {owner: infrastructure}
services:
  x-template: {image: alpine}
  api.v2:
    image: alpine
    x-internal: {nested: secret}
    privileged: true
    depends_on: [db]
    restart: always
`)
	parsed, err := Parse(source)
	require.NoError(t, err)
	require.Len(t, parsed.Services, 1)
	assert.Equal(t, []string{"privileged", "depends_on"}, parsed.Services[0].IgnoredOptions)
	assert.Equal(t, "always", parsed.Services[0].Restart)
}

func TestStrictComposeRejectsInvalidYAML(t *testing.T) {
	for _, tc := range []struct {
		name, content, diagnostic string
		parseError                bool
	}{
		{"direct cycle", `services: {api: &api {image: busybox:1.36, <<: *api}}`, "contains itself", false},
		{"indirect cycle", `services: {api: &api {image: busybox:1.36, <<: &middle {<<: *api}}}`, "contains itself", false},
		{"cyclic merge sequence", `services: {api: &api {image: busybox:1.36, <<: [*api]}}`, "contains itself", false},
		{"alias to its merge sequence", `services: {api: {<<: &merges [*merges]}}`, "merge requires map", false},
		{"cycle in ignored extension", `x-extension: &cycle [*cycle]`, "contains itself", false},
		{"cycle in overridden merge value", `services: {api: {deploy: {}, <<: {deploy: &d {<<: *d}}}}`, "contains itself", false},
		{"excessive aliases", excessiveAliases(), "excessive aliasing", false},
		{"invalid YAML", "services: [", "yaml:", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.content))
			require.ErrorContains(t, err, tc.diagnostic)

			loader := NewFileLoaderWithReader(func(_ context.Context, _ string) ([]byte, error) {
				return []byte(tc.content), nil
			})
			_, err = loader.Load(t.Context(), "/tmp/compose.yaml")
			require.ErrorContains(t, err, tc.diagnostic)
			if tc.parseError {
				require.ErrorAs(t, err, new(*ParseComposeError))
			} else {
				require.ErrorAs(t, err, new(*ValidateComposeError))
			}
		})
	}
}

func excessiveAliases() string {
	var content strings.Builder
	content.WriteString("x-0: &a0 [value]\n")
	for i := 1; i <= 6; i++ {
		aliases := strings.Repeat(fmt.Sprintf("*a%d, ", i-1), 8)
		fmt.Fprintf(&content, "x-%d: &a%d [%s]\n", i, i, aliases)
	}
	return content.String()
}

func TestStrictComposeAcceptsSharedAliases(t *testing.T) {
	parsed, err := Parse([]byte(`services:
  api: &api
    image: busybox:1.36
  worker: *api
  backup: *api
`))
	require.NoError(t, err)
	assert.Len(t, parsed.Services, 3)
}

func TestStrictComposeLoaderRejectsBeforeDeployment(t *testing.T) {
	loader := NewFileLoaderWithReader(func(_ context.Context, _ string) ([]byte, error) {
		return []byte("services: {api: {image: alpine, unexpected: true}}"), nil
	})
	_, err := loader.Load(t.Context(), "/tmp/compose.yaml")
	var validation *ValidateComposeError
	require.True(t, errors.As(err, &validation))
	assert.ErrorContains(t, err, "unexpected")
}
