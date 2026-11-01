package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func prepareComparisonFixture(t *testing.T, service string) Prepared {
	t.Helper()
	parsed, err := compose.Parse([]byte("services:\n  api:\n    image: alpine\n" + service))
	require.NoError(t, err)
	prepared, err := Prepare(compose.File{Compose: *parsed})
	require.NoError(t, err)
	return prepared
}

func TestComparisonNormalizesComposeRepresentations(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"entrypoint", "    entrypoint: /bin/sh -c 'echo hello'\n", "    entrypoint: [/bin/sh, -c, 'echo hello']\n"},
		{"empty entrypoint", "", "    entrypoint: []\n"},
		{"command", "    command: sleep 100\n", "    command: [sleep, '100']\n"},
		{"healthcheck", "    healthcheck: {test: 'echo hello'}\n", "    healthcheck: {test: [CMD-SHELL, 'echo hello']}\n"},
		{"secret", "    secrets: [key]\n", "    secrets: [{source: key, target: /run/secrets/key, uid: '0', gid: '0', mode: 0444}]\n"},
		{"relative secret target", "    secrets: [key]\n", "    secrets: [{source: key, target: key}]\n"},
		{"config", "    configs: [cfg]\n", "    configs: [{source: cfg, target: /cfg}]\n"},
		{"ignored options", "", "    privileged: true\n    depends_on: {db: {condition: service_healthy}}\n    restart: always\n"},
		{"ref ordering", "    secrets: [one, two]\n", "    secrets: [{source: two, target: two}, one]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := prepareComparisonFixture(t, tc.before)
			after := prepareComparisonFixture(t, tc.after)
			assert.Equal(t, before.Digest, after.Digest)
			assert.Empty(t, Compare(before, after))
		})
	}
}

func TestComparisonKeepsMeaningfulChangesAndMasksExecutables(t *testing.T) {
	for _, tc := range []struct{ name, before, after, field string }{
		{"entrypoint", "    entrypoint: [echo, old]\n", "    entrypoint: [echo, new]\n", "entrypoint"},
		{"quoted argument", "    command: [echo, 'a b']\n", "    command: [echo, a, b]\n", "command"},
		{"secret source", "    secrets: [{source: one, target: /run/secrets/key}]\n", "    secrets: [{source: two, target: /run/secrets/key}]\n", "secrets"},
		{"secret target", "    secrets: [key]\n", "    secrets: [{source: key, target: /other}]\n", "secrets"},
		{"secret mode", "    secrets: [key]\n", "    secrets: [{source: key, mode: 0400}]\n", "secrets"},
		{"config target", "    configs: [cfg]\n", "    configs: [{source: cfg, target: /run/secrets/cfg}]\n", "configs"},
		{"restart policy", "    deploy: {restart_policy: {condition: none}}\n", "    deploy: {restart_policy: {condition: any}}\n", "deploy.restart_policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := prepareComparisonFixture(t, tc.before)
			after := prepareComparisonFixture(t, tc.after)
			assert.NotEqual(t, before.Digest, after.Digest)
			changes := Compare(before, after)
			require.NotEmpty(t, changes)
			found := false
			for _, change := range changes {
				found = found || strings.HasPrefix(change.Field, tc.field)
				assert.NotContains(t, change.Field, "is_list")
				assert.NotContains(t, change.Field, "short_form")
			}
			assert.True(t, found, "expected meaningful field %s", tc.field)
		})
	}
	prepared := prepareComparisonFixture(t, "    entrypoint: [echo, 'password=private-value']\n")
	payload, err := json.Marshal(prepared)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "private-value")
}

func TestComparisonIgnoresLegacyMetadataWithoutHidingUserFields(t *testing.T) {
	before := Prepared{Fields: map[string]string{}, Fingerprints: map[string]string{}}
	after := Prepared{Fields: map[string]string{}, Fingerprints: map[string]string{}}
	for _, path := range []string{
		"services/api/entrypoint/IsList", "services/api/command/IsList",
		"services/api/secrets/@key/short_form", "services/api/configs/@cfg/short_form",
		"services/api/Extra/privileged", "services/api/Extra/depends_on/db/condition", "services/api/Extra/restart",
	} {
		before.Fields[path], before.Fingerprints[path] = "true", "old"
	}
	assert.Empty(t, Compare(before, after))
	after.Fields["services/api/environment/Map/IsList"] = "value"
	after.Fingerprints["services/api/environment/Map/IsList"] = "new"
	assert.Len(t, Compare(before, after), 1, "user-defined environment names must remain visible")
}

func TestComparisonDoesNotMutateParserMetadata(t *testing.T) {
	parsed, err := compose.Parse([]byte("services: {api: {image: alpine, entrypoint: 'sleep 100', secrets: [key], privileged: true}}"))
	require.NoError(t, err)
	before, err := json.Marshal(parsed)
	require.NoError(t, err)
	_, err = Prepare(compose.File{Compose: *parsed})
	require.NoError(t, err)
	after, err := json.Marshal(parsed)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
}
