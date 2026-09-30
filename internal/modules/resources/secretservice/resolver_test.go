package secretservice

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestResolverResolveServiceSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		reference  swarm.ServiceSecret
		wantSecret *secretmodel.Secret
	}{
		{
			name:      "resolves by id and preserves target",
			reference: swarm.ServiceSecret{SecretID: "secret-id", SecretName: "old-name", Target: "/run/secrets/db"},
			wantSecret: &secretmodel.Secret{
				ID: "secret-id", Name: "db-password", VersionID: 4,
			},
		},
		{
			name:      "falls back to name for stale id",
			reference: swarm.ServiceSecret{SecretID: "stale-id", SecretName: "db-password"},
			wantSecret: &secretmodel.Secret{
				ID: "secret-id", Name: "db-password", VersionID: 4,
			},
		},
		{
			name:       "keeps missing reference unresolved",
			reference:  swarm.ServiceSecret{SecretID: "missing-id", SecretName: "missing-name"},
			wantSecret: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolver := newTestResolver(t, []secretmodel.Secret{{ID: "secret-id", Name: "db-password", VersionID: 4}}, []service.Info{
				{Name: "api", Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{tt.reference}}},
			})

			resolved, err := resolver.ResolveServiceSecrets(context.Background(), service.Info{
				Name: "api", Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{tt.reference}},
			})
			require.NoError(t, err)
			require.Len(t, resolved, 1)
			assert.Equal(t, tt.reference, resolved[0].Reference)
			assert.Equal(t, tt.wantSecret, resolved[0].Secret)
		})
	}
}

func TestResolverServicesUsingSecret(t *testing.T) {
	t.Parallel()

	secret := secretmodel.Secret{ID: "shared-id", Name: "shared-secret"}
	resolver := newTestResolver(t, []secretmodel.Secret{secret}, []service.Info{
		{
			Name: "worker",
			Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{
				{SecretID: secret.ID, SecretName: secret.Name, Target: "/run/secrets/worker"},
			}},
		},
		{
			Name: "api",
			Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{
				{SecretName: secret.Name, Target: "/run/secrets/api"},
			}},
		},
		{
			Name: "unrelated",
			Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{
				{SecretID: "other", SecretName: "other"},
			}},
		},
	})

	assert.Equal(t, []ServiceUsage{
		{Stack: "payments", Service: "api", Target: "/run/secrets/api"},
		{Stack: "payments", Service: "worker", Target: "/run/secrets/worker"},
	}, resolver.ServicesUsingSecret(secret))
}

func TestResolverServicesUsingSecretUnused(t *testing.T) {
	t.Parallel()

	secret := secretmodel.Secret{ID: "unused-id", Name: "unused"}
	resolver := newTestResolver(t, []secretmodel.Secret{secret}, nil)

	assert.Empty(t, resolver.ServicesUsingSecret(secret))
}

func newTestResolver(
	t *testing.T,
	secrets []secretmodel.Secret,
	services []service.Info,
) *Resolver {
	t.Helper()

	ctx := context.Background()
	filesystem := fs.NewLocalFileSystem()
	secretStore, err := secretstore.NewFileStore(ctx, filepath.Join(t.TempDir(), "secrets.json"), filesystem)
	require.NoError(t, err)
	require.NoError(t, secretStore.Replace(ctx, secrets))

	serviceStore, err := service.NewStore(ctx, filepath.Join(t.TempDir(), "services.json"), filesystem)
	require.NoError(t, err)
	require.NoError(t, serviceStore.ReplaceStack(ctx, "payments", services))

	return NewResolver(serviceStore, secretStore)
}
