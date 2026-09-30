package secretmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	resourceservice "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestControllerAddress(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		rawAddress string
		want       string
		wantError  bool
	}{
		{name: "bind port", rawAddress: ":8001", want: "cloud-secrets:8001"},
		{name: "wildcard ipv4", rawAddress: "0.0.0.0:9000", want: "cloud-secrets:9000"},
		{name: "wildcard ipv6", rawAddress: "[::]:7000", want: "cloud-secrets:7000"},
		{name: "bare port", rawAddress: "6000", want: "cloud-secrets:6000"},
		{name: "missing port", rawAddress: "0.0.0.0", wantError: true},
		{name: "invalid port", rawAddress: ":not-a-port", wantError: true},
		{name: "out of range", rawAddress: ":70000", wantError: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := controllerAddress("cloud-secrets", testCase.rawAddress)
			if testCase.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestResolverUsesPersistedServiceResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := resourceservice.NewStore(ctx, filepath.Join(t.TempDir(), "services.json"), fs.NewLocalFileSystem())
	require.NoError(t, err)
	require.NoError(t, store.ReplaceStack(ctx, "infra", []resourceservice.Info{
		{
			Metadata: metadata.Metadata{Type: stype.SecretManager},
			Name:     "cloud-secrets",
			Image:    "ghcr.io/swarm-deploy/cloud-secrets:v0.4.1",
			Environment: map[string]string{
				cloudSecretsGRPCAddressEnv: "0.0.0.0:8001",
			},
		},
		{
			Metadata: metadata.Metadata{Type: stype.Database},
			Name:     "postgres",
			Image:    "postgres:18",
		},
	}))

	resolved := NewResolver(store).resolve()

	require.Len(t, resolved, 1)
	assert.Equal(t, "infra", resolved[0].stack)
	assert.Equal(t, "cloud-secrets", resolved[0].service)
	assert.Equal(t, cloudSecretsKind, resolved[0].kind)
	assert.True(t, resolved[0].controllable)
	assert.Equal(t, "cloud-secrets:8001", resolved[0].address)
	assert.NoError(t, resolved[0].discoveryError)
}

func TestResolverKeepsClassificationSeparateFromControlCapability(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := resourceservice.NewStore(ctx, filepath.Join(t.TempDir(), "services.json"), fs.NewLocalFileSystem())
	require.NoError(t, err)
	require.NoError(t, store.ReplaceStack(ctx, "infra", []resourceservice.Info{
		{
			Metadata: metadata.Metadata{Type: stype.SecretManager},
			Name:     "cloud-secrets",
			Image:    "ghcr.io/swarm-deploy/cloud-secrets:v0.4.1",
		},
	}))

	resolved := NewResolver(store).resolve()

	require.Len(t, resolved, 1)
	assert.False(t, resolved[0].controllable)
	assert.Empty(t, resolved[0].address)
}
