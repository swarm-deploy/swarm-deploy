package compose

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServicesNormalizeOnDecode(t *testing.T) {
	t.Parallel()

	const raw = `services:
  api:
    image: alpine
    command: sh -c 'echo "a b"'
    entrypoint: /entrypoint.sh
    healthcheck: {test: curl -f http://localhost/}
    cap_add: [net_admin]
    secrets: [token]
    configs: [app]
    x-init-deploy-jobs:
      - name: migrate
        image: alpine
        secrets: [token, {source: cert, target: tls.crt, uid: "0"}]
        configs: [app, {source: nofile, uid: "1000"}]
`
	parsed, err := Parse([]byte(raw))
	require.NoError(t, err)
	payload, err := json.Marshal(parsed.Services, jsonv1.DefaultOptionsV1())
	require.NoError(t, err)
	var restored Services
	require.NoError(t, json.Unmarshal(payload, &restored, jsonv1.DefaultOptionsV1()))

	for name, services := range map[string]Services{"yaml": parsed.Services, "json": restored} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			service := services[0]

			assert.Equal(t, NewCommand([]string{"sh", "-c", `echo "a b"`}), service.Command)
			assert.Equal(t, NewCommand([]string{"/entrypoint.sh"}), service.Entrypoint)
			assert.Equal(t, NewCommand([]string{HealthcheckShell, "curl -f http://localhost/"}), service.Healthcheck.Test)
			assert.Equal(t, Capabilities{"NET_ADMIN"}, service.CapAdd)
			assert.Equal(t, []ObjectRef{
				{Source: "token", Target: "/run/secrets/token"},
				{Source: "cert", Target: "/run/secrets/tls.crt"},
			}, service.InitJobs[0].Secrets)
			assert.Equal(t, []ObjectRef{
				{Source: "app", Target: "/run/secrets/app"},
				{Source: "nofile"},
			}, service.InitJobs[0].Configs)
		})
	}
	// Init Jobs also mount service references with their own defaults, so the
	// parser keeps the source form of service references.
	assert.Equal(t, []ObjectRef{{Source: "token", Target: "/run/secrets/token", ShortForm: true}},
		parsed.Services[0].Secrets)
	assert.Equal(t, []ObjectRef{{Source: "app", Target: "/run/secrets/app", ShortForm: true}},
		parsed.Services[0].Configs)
}

func TestServicesNormalizeLegacySnapshotCommands(t *testing.T) {
	t.Parallel()

	var services Services
	require.NoError(t, json.Unmarshal([]byte(`[{"name":"api","image":"alpine",
		"command":{"Args":["sleep 10"],"IsList":false},"entrypoint":{"Args":[],"IsList":true},
		"healthcheck":{"test":{"Args":["true"],"IsList":false}}}]`), &services, jsonv1.DefaultOptionsV1()))

	assert.Equal(t, NewCommand([]string{"sleep", "10"}), services[0].Command)
	assert.Equal(t, NewCommand(nil), services[0].Entrypoint)
	assert.Equal(t, NewCommand([]string{HealthcheckShell, "true"}), services[0].Healthcheck.Test)
}

func TestServicesRejectInvalidQuotingOnDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, service, expected string
	}{
		{name: "command", service: "command: echo 'secret-value", expected: "services.api.command: invalid quoting"},
		{name: "entrypoint", service: `entrypoint: sh -c "secret-value`, expected: "services.api.entrypoint: invalid quoting"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := "services:\n  api:\n    image: alpine\n    " + tc.service + "\n"

			_, parseErr := Parse([]byte(raw))
			_, loadErr := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
				return []byte(raw), nil
			}).Load(context.Background(), "compose.yaml")

			require.ErrorContains(t, parseErr, tc.expected)
			require.ErrorContains(t, loadErr, tc.expected)
			assert.NotContains(t, parseErr.Error(), "secret-value")
			assert.NotContains(t, loadErr.Error(), "secret-value")
		})
	}
}

func TestParserMetadataIsNotSerialized(t *testing.T) {
	t.Parallel()

	environment, err := NewEnvironment([]string{"Keys=user", "B=2"})
	require.NoError(t, err)
	volume := &ServiceVolume{Type: ServiceVolumeTypeVolume, Source: "data", Target: "/data"}
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{name: "environment", value: *environment, expected: `{"Map":{"B":"2","Keys":"user"}}`},
		{
			name:  "volumes",
			value: ServiceVolumes{Volumes: []*ServiceVolume{volume}, Map: map[string]*ServiceVolume{"/data": volume}},
			expected: `{"Volumes":[{"Type":"volume","Source":"data","Target":"/data","Consistency":"",` +
				`"ReadOnly":false,"Bind":null,"Volume":null,"Tmpfs":null}]}`,
		},
		{
			name:     "networks",
			value:    NewServiceNetworks(&ServiceNetwork{Alias: "backend", ResolvedName: "stack_backend"}),
			expected: `{"List":[{"Alias":"backend","ResolvedName":"stack_backend"}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload, err := json.Marshal(tc.value, jsonv1.DefaultOptionsV1())

			require.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(payload))
		})
	}
}
