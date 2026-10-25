package secretservice

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	servicemodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestResolverResolveServiceSecrets(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

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
				CreatedAt: timestamp, UpdatedAt: timestamp,
			},
		},
		{
			name:      "falls back to name for stale id",
			reference: swarm.ServiceSecret{SecretID: "stale-id", SecretName: "db-password"},
			wantSecret: &secretmodel.Secret{
				ID: "secret-id", Name: "db-password", VersionID: 4,
				CreatedAt: timestamp, UpdatedAt: timestamp,
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

			resolver := newTestResolver(t, []secretmodel.Secret{{
				ID: "secret-id", Name: "db-password", VersionID: 4,
				CreatedAt: timestamp, UpdatedAt: timestamp,
			}}, []servicemodel.Info{
				{Name: "api", Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{tt.reference}}},
			})

			resolved, err := resolver.ResolveServiceSecrets(context.Background(), servicemodel.Info{
				Name: "api", Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{tt.reference}},
			})
			require.NoError(t, err)
			require.Len(t, resolved, 1)
			assert.Equal(t, tt.reference, resolved[0].Reference)
			assert.Equal(t, tt.wantSecret, resolved[0].Secret)
		})
	}
}

func TestResolverResolveServiceSecretsReturnsAllReferencesOnStoreError(t *testing.T) {
	t.Parallel()

	secretDB, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	secrets := secretstore.NewSQLStore(secretDB)
	require.NoError(t, secretDB.Close())
	services := servicestore.NewSQLStore(testutil.OpenSQLite(t))
	references := []swarm.ServiceSecret{
		{SecretID: "first-id", SecretName: "first", Target: "/run/secrets/first"},
		{SecretID: "second-id", SecretName: "second", Target: "/run/secrets/second"},
	}
	resolver := NewResolver(services, secrets)

	resolved, err := resolver.ResolveServiceSecrets(t.Context(), servicemodel.Info{
		Spec: swarm.ServiceSpec{Secrets: references},
	})
	require.Error(t, err)
	require.Len(t, resolved, len(references))
	for index, reference := range references {
		assert.Equal(t, reference, resolved[index].Reference)
		assert.Nil(t, resolved[index].Secret)
	}
}

func TestResolverServicesUsingSecret(t *testing.T) {
	t.Parallel()

	secret := secretmodel.Secret{ID: "shared-id", Name: "shared-secret"}
	resolver := newTestResolver(t, []secretmodel.Secret{secret}, []servicemodel.Info{
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

	usedBy, err := resolver.ServicesUsingSecret(t.Context(), secret)
	require.NoError(t, err)
	assert.Equal(t, []ServiceUsage{
		{Stack: "payments", Service: "api", Target: "/run/secrets/api"},
		{Stack: "payments", Service: "worker", Target: "/run/secrets/worker"},
	}, usedBy)
}

func TestResolverServicesUsingSecretUnused(t *testing.T) {
	t.Parallel()

	secret := secretmodel.Secret{ID: "unused-id", Name: "unused"}
	resolver := newTestResolver(t, []secretmodel.Secret{secret}, nil)

	usedBy, err := resolver.ServicesUsingSecret(t.Context(), secret)
	require.NoError(t, err)
	assert.Empty(t, usedBy)
}

func newTestResolver(
	t *testing.T,
	secrets []secretmodel.Secret,
	services []servicemodel.Info,
) *Resolver {
	t.Helper()

	ctx := context.Background()
	db := testutil.OpenSQLite(t)
	secretStore := secretstore.NewSQLStore(db)
	for i := range secrets {
		if secrets[i].CreatedAt.IsZero() {
			secrets[i].CreatedAt = time.Now().UTC()
		}
		if secrets[i].UpdatedAt.IsZero() {
			secrets[i].UpdatedAt = secrets[i].CreatedAt
		}
	}
	require.NoError(t, secretStore.Replace(ctx, secrets))

	serviceStore := servicestore.NewSQLStore(db)
	require.NoError(t, serviceStore.ReplaceStack(ctx, "payments", services))

	return NewResolver(serviceStore, secretStore)
}
