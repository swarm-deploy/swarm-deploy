package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestEffectiveDigestsAreStableAcrossMapOrderAndEquivalentYAML(t *testing.T) {
	first, err := compose.Parse([]byte(`
services:
  api:
    image: example/api:1
    environment:
      MODE: production
      LOG_LEVEL: info
    labels:
      app.example/role: api
      app.example/tier: backend
    secrets: [token]
secrets:
  token:
    external: true
`))
	require.NoError(t, err)
	second, err := compose.Parse([]byte(`
secrets:
  token: {external: true}
services:
  api:
    secrets:
      - source: token
        target: /run/secrets/token
        uid: "0"
        gid: "0"
        mode: 0444
    labels:
      app.example/tier: backend
      app.example/role: api
    environment:
      LOG_LEVEL: info
      MODE: production
    image: example/api:1
`))
	require.NoError(t, err)

	before, err := Prepare(compose.File{Compose: *first})
	require.NoError(t, err)
	after, err := Prepare(compose.File{Compose: *second})
	require.NoError(t, err)

	assert.Equal(t, before.Digest, after.Digest)
	assert.Equal(t, before.StackResourcesDigest, after.StackResourcesDigest)
	assert.Equal(t, before.ServiceDigests, after.ServiceDigests)
	assert.Equal(t, DigestAlgorithmVersion, before.DigestVersion)
}

func TestServiceChangeOnlyChangesAffectedHashes(t *testing.T) {
	beforeFile := compose.File{Compose: compose.Compose{Services: compose.Services{
		{Name: "api", Image: "example/api:1", Environment: compose.Environment{Map: map[string]string{"MODE": "one"}}},
		{Name: "worker", Image: "example/worker:1"},
	}}}
	afterFile := beforeFile
	afterFile.Compose.Services = append(compose.Services{}, beforeFile.Compose.Services...)
	afterFile.Compose.Services[0].Environment = compose.Environment{Map: map[string]string{"MODE": "two"}}

	before, err := Prepare(beforeFile)
	require.NoError(t, err)
	after, err := Prepare(afterFile)
	require.NoError(t, err)

	assert.NotEqual(t, before.Digest, after.Digest)
	assert.NotEqual(t, before.ServiceDigests["api"], after.ServiceDigests["api"])
	assert.Equal(t, before.ServiceDigests["worker"], after.ServiceDigests["worker"])
	assert.Equal(t, before.StackResourcesDigest, after.StackResourcesDigest)
}

func TestConfigAndSecretFingerprintsAffectOnlyConsumers(t *testing.T) {
	newDesired := func() compose.File {
		return compose.File{Compose: compose.Compose{
			Services: compose.Services{
				{Name: "api", Image: "example/api:1", Configs: []compose.ObjectRef{{Source: "settings"}}, Secrets: []compose.ObjectRef{{Source: "token"}}},
				{Name: "worker", Image: "example/worker:1"},
			},
			Configs: compose.Configs{"settings": {Alias: "settings", File: "settings.yaml", Data: []byte("version: one\n")}},
			Secrets: compose.Secrets{"token": {Alias: "token", File: "token", Data: []byte("secret-one")}},
		}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(compose.File)
	}{
		{name: "config", mutate: func(desired compose.File) {
			desired.Compose.Configs["settings"].Data = []byte("version: two\n")
		}},
		{name: "secret", mutate: func(desired compose.File) {
			desired.Compose.Secrets["token"].Data = []byte("secret-two")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desired := newDesired()
			before, err := Prepare(desired)
			require.NoError(t, err)
			tc.mutate(desired)
			after, err := Prepare(desired)
			require.NoError(t, err)

			assert.NotEqual(t, before.ServiceDigests["api"], after.ServiceDigests["api"])
			assert.Equal(t, before.ServiceDigests["worker"], after.ServiceDigests["worker"])
			assert.NotEqual(t, before.StackResourcesDigest, after.StackResourcesDigest)
			payload, err := json.Marshal(after)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), "secret-two")
		})
	}
}

func TestExternallyManagedReplicasDoNotChangeDigest(t *testing.T) {
	firstReplicas := uint64(1)
	secondReplicas := uint64(9)
	service := compose.Service{Name: "api", Image: "example/api:1", Deploy: compose.ServiceDeploy{
		Replicas: &firstReplicas,
		Labels: *compose.NewLabels(map[string]string{
			labelsdict.ServiceSyncPolicyReplicasManagedLabelKey: "false",
		}),
	}}
	before, err := Prepare(compose.File{Compose: compose.Compose{Services: compose.Services{service}}})
	require.NoError(t, err)
	service.Deploy.Replicas = &secondReplicas
	after, err := Prepare(compose.File{Compose: compose.Compose{Services: compose.Services{service}}})
	require.NoError(t, err)

	assert.Equal(t, before.Digest, after.Digest)
	assert.Equal(t, before.ServiceDigests["api"], after.ServiceDigests["api"])
}
