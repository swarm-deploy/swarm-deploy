package compose_test

import (
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestServiceDigestsUsesCurrentComposeWithoutUpdatingSource(t *testing.T) {
	loader := compose.NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
		return []byte("services: {api: {image: api:1}}"), nil
	})
	file, err := loader.Load(t.Context(), "compose.yaml")
	require.NoError(t, err)
	require.Nil(t, file.ServiceDigests)
	sourceDigest := file.Digest
	require.NotEmpty(t, sourceDigest)

	before, err := compose.ServiceDigests(file)
	require.NoError(t, err)
	require.NotEmpty(t, before["api"])

	file.Compose.Services[0].Image = "api:2"
	after, err := compose.ServiceDigests(file)
	require.NoError(t, err)
	assert.NotEqual(t, before["api"], after["api"])
	assert.Equal(t, sourceDigest, file.Digest)
	assert.Nil(t, file.ServiceDigests)
}

func TestServiceDigestsReturnsNoDigestsOnError(t *testing.T) {
	file := parseDigestCompose(t, `
services:
  worker:
    image: worker:1
  api:
    image: api:1
`)
	file.Compose.Services[1].Command = compose.Command{Args: []string{"echo 'unterminated"}}

	digests, err := compose.ServiceDigests(file)
	require.Error(t, err)
	assert.Nil(t, digests)
}

func TestDesiredServiceDigestTracksEffectiveDependencies(t *testing.T) {
	base := desiredDigestFixture()
	baseline, err := compose.ServiceDigests(base)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*compose.File)
	}{
		{name: "service image", mutate: func(file *compose.File) { file.Compose.Services[0].Image = "api:2" }},
		{name: "service environment", mutate: func(file *compose.File) {
			file.Compose.Services[0].Environment.Map["MODE"] = "changed"
		}},
		{name: "deployment settings", mutate: func(file *compose.File) {
			replicas := uint64(3)
			file.Compose.Services[0].Deploy.Replicas = &replicas
		}},
		{name: "config contents", mutate: func(file *compose.File) {
			file.Compose.Configs["settings"].Data = []byte("changed config")
		}},
		{name: "secret contents", mutate: func(file *compose.File) {
			file.Compose.Secrets["token"].Data = []byte("changed secret")
		}},
		{name: "network definition", mutate: func(file *compose.File) {
			network := file.Compose.Networks["backend"]
			network.Driver = "custom"
			file.Compose.Networks["backend"] = network
		}},
		{name: "volume definition", mutate: func(file *compose.File) {
			file.Compose.Volumes["data"].DriverOpts["device"] = "/changed"
		}},
		{name: "init job", mutate: func(file *compose.File) {
			file.Compose.Services[0].InitJobs[0].Image = "migrate:2"
		}},
		{name: "init job config", mutate: func(file *compose.File) {
			file.Compose.Configs["migration"].Data = []byte("changed migration")
		}},
		{name: "init job secret", mutate: func(file *compose.File) {
			file.Compose.Secrets["migration-token"].Data = []byte("changed migration secret")
		}},
		{name: "init job network", mutate: func(file *compose.File) {
			network := file.Compose.Networks["jobs"]
			network.Internal = boolPointer(true)
			file.Compose.Networks["jobs"] = network
		}},
		{name: "credential config", mutate: func(file *compose.File) {
			file.Compose.Configs["credential"].Data = []byte("changed credential")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := desiredDigestFixture()
			test.mutate(candidate)
			digests, digestErr := compose.ServiceDigests(candidate)
			require.NoError(t, digestErr)
			assert.NotEqual(t, baseline["api"], digests["api"])
			assert.Equal(t, baseline["worker"], digests["worker"], "unrelated service digest changed")
		})
	}
}

func TestDesiredServiceDigestIgnoresSourceOnlyAndUnreferencedChanges(t *testing.T) {
	baseline, err := compose.ServiceDigests(desiredDigestFixture())
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*compose.File)
	}{
		{name: "config source path", mutate: func(file *compose.File) {
			file.Compose.Configs["settings"].File = "moved/settings.conf"
		}},
		{name: "secret source path", mutate: func(file *compose.File) {
			file.Compose.Secrets["token"].File = "moved/token"
		}},
		{name: "unreferenced config", mutate: func(file *compose.File) {
			file.Compose.Configs["unused"].Data = []byte("changed unused config")
		}},
		{name: "unreferenced secret", mutate: func(file *compose.File) {
			file.Compose.Secrets["unused"].Data = []byte("changed unused secret")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := desiredDigestFixture()
			test.mutate(candidate)
			digests, digestErr := compose.ServiceDigests(candidate)
			require.NoError(t, digestErr)
			assert.Equal(t, baseline, digests)
		})
	}
}

func TestDesiredServiceDigestUsesReferencedFileFingerprints(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		clear    func(*compose.File)
	}{
		{name: "config", resource: "configs/settings", clear: func(file *compose.File) {
			file.Compose.Configs["settings"].Data = nil
		}},
		{name: "secret", resource: "secrets/token", clear: func(file *compose.File) {
			file.Compose.Secrets["token"].Data = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := desiredDigestFixture()
			test.clear(before)
			before.ResourceDigests[test.resource] = "first-fingerprint"
			baseline, err := compose.ServiceDigests(before)
			require.NoError(t, err)

			after := desiredDigestFixture()
			test.clear(after)
			after.ResourceDigests[test.resource] = "second-fingerprint"
			changed, err := compose.ServiceDigests(after)
			require.NoError(t, err)
			assert.NotEqual(t, baseline["api"], changed["api"])
			assert.Equal(t, baseline["worker"], changed["worker"])
		})
	}
}

func TestDesiredServiceDigestNormalizesEquivalentCompose(t *testing.T) {
	left := parseDigestCompose(t, `
services:
  api:
    image: api:1
    command: echo hello
    cap_add: [net_admin, CAP_SYS_TIME]
    ports: ["8080:80", "8443:443"]
    configs: [settings]
    secrets: [token]
configs:
  settings: {external: true, name: settings}
secrets:
  token: {external: true, name: token}
`)
	right := parseDigestCompose(t, `
services:
  api:
    image: api:1
    command: [echo, hello]
    cap_add: [sys_time, CAP_NET_ADMIN]
    ports: ["8443:443", "8080:80"]
    networks: [default]
    configs: [{source: settings, target: /settings, uid: "0", gid: "0", mode: 0444}]
    secrets: [{source: token, target: /run/secrets/token, uid: "0", gid: "0", mode: 0444}]
configs:
  settings: {external: true, name: settings}
secrets:
  token: {external: true, name: token}
`)

	leftDigest, err := compose.ServiceDigests(left)
	require.NoError(t, err)
	rightDigest, err := compose.ServiceDigests(right)
	require.NoError(t, err)
	assert.Equal(t, leftDigest, rightDigest)
}

func TestDesiredServiceDigestPreservesMountOrder(t *testing.T) {
	const definition = `
services:
  api:
    image: api:1
    volumes:
      - ./root:/app
      - ./config:/app/config
    secrets: [a, b]
    configs: [a, b]
    x-init-deploy-jobs:
      - name: migrate
        image: migrate:1
        secrets: [job-a, job-b]
        configs: [job-a, job-b]
secrets:
  a: {external: true}
  b: {external: true}
  job-a: {external: true}
  job-b: {external: true}
configs:
  a: {external: true}
  b: {external: true}
  job-a: {external: true}
  job-b: {external: true}
`
	baseline, err := compose.ServiceDigests(parseDigestCompose(t, definition))
	require.NoError(t, err)

	tests := []struct {
		name    string
		reorder func(*compose.Service)
	}{
		{name: "service volumes", reorder: func(service *compose.Service) { slices.Reverse(service.Volumes.Volumes) }},
		{name: "service secrets", reorder: func(service *compose.Service) { slices.Reverse(service.Secrets) }},
		{name: "service configs", reorder: func(service *compose.Service) { slices.Reverse(service.Configs) }},
		{name: "init job secrets", reorder: func(service *compose.Service) { slices.Reverse(service.InitJobs[0].Secrets) }},
		{name: "init job configs", reorder: func(service *compose.Service) { slices.Reverse(service.InitJobs[0].Configs) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := parseDigestCompose(t, definition)
			test.reorder(&candidate.Compose.Services[0])

			digests, err := compose.ServiceDigests(candidate)
			require.NoError(t, err)
			assert.NotEqual(t, baseline["api"], digests["api"])
		})
	}
}

func TestDesiredServiceDigestUsesInitJobConfigTargetSemantics(t *testing.T) {
	short := parseDigestCompose(t, `
services:
  api:
    image: api:1
    x-init-deploy-jobs:
      - name: migrate
        image: migrate:1
        configs: [settings]
configs:
  settings: {external: true, name: settings}
`)
	explicit := parseDigestCompose(t, `
services:
  api:
    image: api:1
    x-init-deploy-jobs:
      - name: migrate
        image: migrate:1
        configs: [{source: settings, target: /settings}]
configs:
  settings: {external: true, name: settings}
`)
	different := parseDigestCompose(t, `
services:
  api:
    image: api:1
    x-init-deploy-jobs:
      - name: migrate
        image: migrate:1
        configs: [{source: settings, target: /etc/settings}]
configs:
  settings: {external: true, name: settings}
`)

	shortDigest, err := compose.ServiceDigests(short)
	require.NoError(t, err)
	explicitDigest, err := compose.ServiceDigests(explicit)
	require.NoError(t, err)
	differentDigest, err := compose.ServiceDigests(different)
	require.NoError(t, err)
	assert.Equal(t, shortDigest, explicitDigest)
	assert.NotEqual(t, explicitDigest, differentDigest)
}

func TestDesiredServiceDigestPreservesInitJobOrderAndDoesNotMutateCompose(t *testing.T) {
	desired := desiredDigestFixture()
	desired.Compose.Services[0].InitJobs = append(desired.Compose.Services[0].InitJobs,
		compose.InitJob{Name: "seed", Image: "seed:1"})
	before, err := json.Marshal(desired, json.Deterministic(true))
	require.NoError(t, err)
	first, err := compose.ServiceDigests(desired)
	require.NoError(t, err)
	second, err := compose.ServiceDigests(desired)
	require.NoError(t, err)
	after, err := json.Marshal(desired, json.Deterministic(true))
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, before, after, "digest calculation mutated prepared Compose")
	assert.Len(t, first["api"], sha256HexLength)
	_, err = hex.DecodeString(first["api"])
	require.NoError(t, err)
	assert.NotContains(t, first["api"], "service-secret")

	desired.Compose.Services[0].InitJobs[0], desired.Compose.Services[0].InitJobs[1] =
		desired.Compose.Services[0].InitJobs[1], desired.Compose.Services[0].InitJobs[0]
	reordered, err := compose.ServiceDigests(desired)
	require.NoError(t, err)
	assert.NotEqual(t, first["api"], reordered["api"])
}

const sha256HexLength = 64

func boolPointer(value bool) *bool { return &value }

func desiredDigestFixture() *compose.File {
	serviceNetwork := compose.NewServiceNetworks(&compose.ServiceNetwork{Alias: "backend"})
	jobNetwork := compose.NewServiceNetworks(&compose.ServiceNetwork{Alias: "jobs"})
	mount := &compose.ServiceVolume{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/data"}
	return &compose.File{
		ResourceDigests: map[string]string{},
		Compose: compose.Compose{
			Services: compose.Services{
				{
					Name: "api", Image: "api:1", Networks: serviceNetwork,
					Environment:    compose.Environment{Map: map[string]string{"MODE": "service-secret"}},
					Configs:        []compose.ObjectRef{{Source: "settings"}},
					Secrets:        []compose.ObjectRef{{Source: "token"}},
					CredentialSpec: &compose.CredentialSpec{Config: "credential"},
					Volumes: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{mount},
						Map: map[string]*compose.ServiceVolume{"/data": mount}},
					InitJobs: []compose.InitJob{{
						Name: "migrate", Image: "migrate:1", Networks: jobNetwork,
						Configs: []compose.ObjectRef{{Source: "migration"}},
						Secrets: []compose.ObjectRef{{Source: "migration-token"}},
					}},
				},
				{Name: "worker", Image: "worker:1"},
			},
			Networks: map[string]compose.Network{
				"backend": {Driver: "overlay"},
				"jobs":    {Driver: "overlay"},
			},
			Configs: compose.Configs{
				"settings":   {Alias: "settings", File: "settings.conf", Data: []byte("config")},
				"migration":  {Alias: "migration", File: "migration.sql", Data: []byte("migration")},
				"credential": {Alias: "credential", File: "credential.json", Data: []byte("credential")},
				"unused":     {Alias: "unused", File: "unused.conf", Data: []byte("unused")},
			},
			Secrets: compose.Secrets{
				"token":           {Alias: "token", File: "token", Data: []byte("secret")},
				"migration-token": {Alias: "migration-token", File: "migration-token", Data: []byte("migration-secret")},
				"unused":          {Alias: "unused", File: "unused-secret", Data: []byte("unused-secret")},
			},
			Volumes: compose.Volumes{
				"data": {Alias: "data", Driver: "local", DriverOpts: map[string]string{"device": "/data"}},
			},
		},
	}
}

func parseDigestCompose(t *testing.T, raw string) *compose.File {
	t.Helper()
	parsed, err := compose.Parse([]byte(raw))
	require.NoError(t, err)
	return &compose.File{Compose: *parsed}
}
