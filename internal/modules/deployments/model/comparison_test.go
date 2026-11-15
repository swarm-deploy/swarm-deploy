package model

import (
	"crypto/sha256"
	"encoding/hex"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/compose/composetest"
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
		{"ignored options", "", "    privileged: true\n    depends_on: {db: {condition: service_healthy}}\n"},
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
		{"legacy restart", "    restart: always\n", "    restart: on-failure:5\n", "restart"},
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
		"services/api/Extra/privileged", "services/api/Extra/depends_on/db/condition",
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

// TODO(0.16.0): Remove this compatibility check once the digest encoding changes.
func TestPreparePreservesExistingDigestForUnaffectedCompose(t *testing.T) {
	parsed, err := compose.Parse([]byte(`services:
  api:
    image: alpine:3
    command: [echo, hello]
    environment: {MODE: production}
    ports: ["8080:80"]
    volumes: [data:/data, ./config:/config:ro]
volumes: {data: {}}
`))
	require.NoError(t, err)
	prepared, err := Prepare(compose.File{Path: "compose.yaml", Digest: "source-digest", Compose: *parsed})
	require.NoError(t, err)
	assert.Equal(t, "352941afe43fced7b8cc82626ecddd629436b76778cb824fceff57b0fc370b80", prepared.Digest)
}

// TestPreparePinsDesiredStateDigests pins desired-state digests and snapshot
// fields. Fixtures with Init Job configs use the same default target as
// service configs. The other fixtures retain their persisted formats.
// TODO(0.16.0): Refresh the expected digests after changing the digest encoding.
func TestPreparePinsDesiredStateDigests(t *testing.T) {
	expected := map[string]struct{ digest, fields string }{
		"service object references": {
			"004df134462da6f5f3aff343203e9e4d97c4236dc329790f6f9a13837e9a92f5",
			"751e41b7de55924cfd32deca894938504e0674aba12338034683953e2bf268b0",
		},
		"init job object references": {
			"06e7f0fc93f079d97182c61cab368b456cbee9da74cddba73b593a9bfd252a46",
			"41471c061ba36d6a1b67400b47b32b94c30568cc0b211f159331070af5c56aa4",
		},
		"execution syntax": {
			"bdb1f473f291452616f95ef0ce30028c540a83cfe0eada471303319625962ebb",
			"8865ed85f400a719411808907ee476779a623052142bc915e9837881c4ec5a15",
		},
		"environment and labels named like parser metadata": {
			"4c184ab6ae19f23b0fbdbfde1028bc6d428882c3bbd45cc7fb072cc04ffbfe98",
			"9b8e8ad0204532df2fdd652d599cb7a40ac137b4d126d7be8f09354745ec87cd",
		},
		"capabilities": {
			"fa7286269c7a89a513997b3bddc5f1ccdee5c84334510838c23062005b1275b5",
			"8db26ba189bf68667a31eace2722649d9eba247471e4204e67ff5b55baef3f63",
		},
		"service ordering": {
			"4d5d867999d52fa965c857f60b31fb1246cc6dfe2939dc5e5d82e61f80ea85b4",
			"d40f142de6f16f228fe98d7ab5e96b7a5152bd6babc6f57a637bc6f5ae4bece8",
		},
		"loaded mounts, ports and networks": {
			"7ffdfdf3f8ad39f8fc8bdfbee05d576f2e9aaddc45bc72a752c119b208ad703d",
			"f295c05f8ffbf23198ce59739573261a719ad46ea878520479993555f291b0cc",
		},
		"programmatic model": {
			"945194d630e949058dfb5468c475dcf22cceb2b9c1cc0bc4985efed35fc97be8",
			"d404d6253d9f8225187b803c31635baec4d7af0e9dde9438ca5651bffaa8530f",
		},
		"programmatic zero values": {
			"e7c28d8d81369991bbc40b18b104eaa3ca37bbc1edcf5788c2b682b32ea04f10",
			"ebcecf58608d0177d2b4d1ce991a3239947961879e5d349e9a95a9986ea6fe5d",
		},
	}
	fixtures := composetest.CanonicalFixtures()
	require.Len(t, expected, len(fixtures))
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			file, err := fixture.Load()
			require.NoError(t, err)

			prepared, err := Prepare(file)
			require.NoError(t, err)

			assert.Equal(t, expected[fixture.Name].digest, prepared.Digest)
			fields, err := json.Marshal(struct {
				F, P map[string]string
				R    map[string]bool
			}{prepared.Fields, prepared.Fingerprints, prepared.Redacted}, jsonv1.DefaultOptionsV1())
			require.NoError(t, err)
			sum := sha256.Sum256(fields)
			assert.Equal(t, expected[fixture.Name].fields, hex.EncodeToString(sum[:]))
		})
	}
}

func TestPreparedSnapshotOmitsParserMetadata(t *testing.T) {
	prepared := prepareComparisonFixture(t, "    command: sleep 10\n    healthcheck: {test: 'true'}\n"+
		"    secrets: [key]\n    configs: [cfg]\n    environment: {MODE: production}\n"+
		"    volumes: [data:/data]\n    networks: [backend]\n")

	payload, err := json.Marshal(prepared.Definition, jsonv1.DefaultOptionsV1())
	require.NoError(t, err)

	for _, metadata := range []string{"IsList", "short_form", "Keys", "AliasMap", `"Names"`, `"Aliases"`, `"Map":{"/data"`} {
		assert.NotContains(t, string(payload), metadata)
	}
	service := prepared.Definition.Compose.Services[0]
	assert.Equal(t, []string{"****", "****"}, service.Command.Args, "commands are opaque")
	assert.True(t, service.Command.IsList())
}

func TestComparisonShowsUserEntriesNamedAlias(t *testing.T) {
	prepare := func(value string) Prepared {
		parsed, err := compose.Parse([]byte(`services:
  api:
    image: alpine
    labels: {Alias: ` + value + `}
    environment: {Alias: ` + value + `}
    networks: [backend]
    configs: [app]
configs: {app: {external: true}}
`))
		require.NoError(t, err)
		prepared, err := Prepare(compose.File{Compose: *parsed})
		require.NoError(t, err)
		return prepared
	}
	before, after := prepare("old"), prepare("new")

	assert.Equal(t, "old", before.Fields["services/api/labels/Map/Alias"])
	assert.Equal(t, "old", before.Fields["services/api/environment/Map/Alias"])
	assert.NotContains(t, before.Fields, "services/api/networks/List/@backend/Alias", "identity is in the path")
	assert.NotContains(t, before.Fields, "configs/app/Alias", "identity is in the path")
	fields := []string{}
	for _, change := range Compare(before, after) {
		fields = append(fields, change.Field)
	}
	assert.ElementsMatch(t, []string{"labels.alias", "environment.alias"}, fields)
}

func TestPreparedSnapshotDecodesLegacyComposeMetadata(t *testing.T) {
	payload := []byte(`{
  "definition": {"path":"compose.yaml","compose":{"services":[{
    "name":"api","image":"alpine","command":{"Args":["****"],"IsList":true},
    "entrypoint":{"Args":null,"IsList":true},"cap_add":["SYS_ADMIN"],
    "environment":{"Map":{"Keys":"user-value"},"Keys":null},
    "labels":{"Map":null},"deploy":{"labels":{"Map":null}},"logging":{},
    "volumes":{"Volumes":null,"Map":null}
  }],"networks":null,"configs":null,"secrets":null,"volumes":null}},
  "digest":"legacy-digest","fields":{"services/api/image":"alpine"},
  "fingerprints":{"services/api/image":"legacy-fingerprint"},"redacted":{}
}`)
	var prepared Prepared
	require.NoError(t, json.Unmarshal(payload, &prepared))
	require.Len(t, prepared.Definition.Compose.Services, 1)
	service := prepared.Definition.Compose.Services[0]
	assert.Equal(t, compose.Capabilities{"SYS_ADMIN"}, service.CapAdd)
	assert.Equal(t, "user-value", service.Environment.Map["Keys"])
	assert.Equal(t, "legacy-digest", prepared.Digest)
	assert.Empty(t, Compare(prepared, prepared))
}

// TODO(0.16.0): Remove this compatibility check once the digest encoding changes.
func TestDesiredDigestMatchesPersistedBaseline(t *testing.T) {
	prepared := prepareComparisonFixture(t, "    command: [echo, '<hello>']\n"+
		"    labels: {zebra: last, alpha: first}\n"+
		"    healthcheck: {test: 'true', retries: 0}\n"+
		"    read_only: true\n")
	// This digest was recorded before migrating the JSON encoder.
	assert.Equal(t, "8f9f0c431808ba6fced58eafc7b2a045be2bb27aa8e0a1c2d072cdbe5ec76786", prepared.Digest)
}
