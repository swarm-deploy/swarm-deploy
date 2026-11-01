package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictComposeRejectsUnsupportedFields(t *testing.T) {
	for _, tc := range []struct{ name, content, field string }{
		{"root", "include: other.yaml", "include"},
		{"service", "services: {api: {image: alpine, build: .}}", "build"},
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
  api:
    image: alpine
    x-internal: {nested: secret}
    privileged: true
    depends_on: [db]
    restart: always
`)
	parsed, err := Parse(source)
	require.NoError(t, err)
	require.Len(t, parsed.Services, 1)
	assert.Equal(t, []string{"privileged", "depends_on", "restart"}, parsed.Services[0].IgnoredOptions)
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
