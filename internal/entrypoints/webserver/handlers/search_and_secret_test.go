package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretservice"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	webroute "github.com/swarm-deploy/webroute/api"
)

func TestHandlerGetSecretByName(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	secretsReader := newSecretsStore(t, []secretmodel.Secret{
		{
			ID: "secret-id", Name: "db-password", VersionID: 7,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now, Driver: "vault",
			ExternalPath: "kv/prod/db-password", ExternalVersionID: "v12",
			Labels: map[string]string{
				"external_path": "kv/prod/db-password", "external_version_id": "v12", "scope": "production",
			},
		},
	})
	servicesStore := newServiceStore(t)
	require.NoError(t, servicesStore.ReplaceStack(context.Background(), "payments", []service.Info{{
		Name: "api",
		Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{{
			SecretID: "secret-id", SecretName: "db-password", Target: "/run/secrets/db-password",
		}}},
	}}))
	h := &handler{
		secrets:         secretsReader,
		secretRelations: secretservice.NewResolver(servicesStore, secretsReader),
	}

	resp, err := h.GetSecretByName(context.Background(), generated.GetSecretByNameParams{Name: "db-password"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "secret-id", resp.ID)
	assert.Equal(t, "db-password", resp.Name)
	assert.Equal(t, int64(7), resp.VersionID)
	assert.True(t, resp.Driver.IsSet())
	assert.Equal(t, "vault", resp.Driver.Value)
	assert.True(t, resp.External.IsSet())
	assert.Equal(t, "kv/prod/db-password", resp.External.Value.Path.Value)
	assert.Equal(t, "v12", resp.External.Value.VersionID.Value)
	assert.True(t, resp.Labels.IsSet())
	assert.Equal(t, "production", resp.Labels.Value["scope"])
	require.Len(t, resp.UsedBy, 1)
	assert.Equal(t, "payments", resp.UsedBy[0].Stack)
	assert.Equal(t, "api", resp.UsedBy[0].Service)
	assert.Equal(t, "/run/secrets/db-password", resp.UsedBy[0].Target.Value)
}

func TestHandlerGetSecretByName_NotFound(t *testing.T) {
	t.Parallel()

	secretsReader := newSecretsStore(t, []secretmodel.Secret{{Name: "known-secret"}})
	h := &handler{
		secrets:         secretsReader,
		secretRelations: secretservice.NewResolver(newServiceStore(t), secretsReader),
	}

	_, err := h.GetSecretByName(context.Background(), generated.GetSecretByNameParams{Name: "unknown-secret"})
	require.Error(t, err)

	var sErr *statusError
	require.True(t, errors.As(err, &sErr))
	assert.Equal(t, 404, sErr.code)
}

func TestHandlerListSecretsIncludesServiceUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	secretsReader := newSecretsStore(t, []secretmodel.Secret{{ID: "secret-id", Name: "db-password"}})
	servicesStore := newServiceStore(t)
	require.NoError(t, servicesStore.ReplaceStack(ctx, "payments", []service.Info{{
		Name: "api",
		Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{{
			SecretID: "secret-id", SecretName: "db-password", Target: "/run/secrets/db-password",
		}}},
	}}))
	h := &handler{
		secrets:         secretsReader,
		secretRelations: secretservice.NewResolver(servicesStore, secretsReader),
	}

	resp, err := h.ListSecrets(ctx)
	require.NoError(t, err)
	require.Len(t, resp.Secrets, 1)
	require.Len(t, resp.Secrets[0].UsedBy, 1)
	assert.Equal(t, "payments", resp.Secrets[0].UsedBy[0].Stack)
	assert.Equal(t, "api", resp.Secrets[0].UsedBy[0].Service)
	assert.Equal(t, "/run/secrets/db-password", resp.Secrets[0].UsedBy[0].Target.Value)
}

func TestHandlerSearch_PriorityAndDedupe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	servicesStore, err := service.NewStore(ctx, t.TempDir()+"/services.json", fs.NewLocalFileSystem())
	require.NoError(t, err)
	require.NoError(t, servicesStore.ReplaceStack(ctx, "payments", []service.Info{
		{
			Name:     "api-app",
			Metadata: metadata.Metadata{Type: "application"},
			WebRoutes: []webroute.WebRoute{
				{From: webroute.Address{Domain: "api-app.example.com", Address: "10.10.0.5", Port: "443"}},
			},
		},
		{
			Name:     "billing",
			Metadata: metadata.Metadata{Type: "application"},
			WebRoutes: []webroute.WebRoute{
				{From: webroute.Address{Domain: "billing.example.com", Address: "10.10.0.7", Port: "443"}},
			},
		},
	}))

	secretsReader := newSecretsStore(t, []secretmodel.Secret{{Name: "api-app-secret"}})
	h := &handler{
		stackProvider: newConfigWithStacks([]config.StackSpec{}),
		services:      servicesStore,
		secrets:       secretsReader,
	}

	resp, err := h.Search(context.Background(), generated.SearchParams{Query: "api-app"})
	require.NoError(t, err)
	require.Len(t, resp.Results, 2)

	assert.Equal(t, generated.SearchResultMatchServiceName, resp.Results[0].Match)
	assert.Equal(t, generated.SearchResultKindService, resp.Results[0].Kind)
	assert.Equal(t, "api-app", resp.Results[0].Label)

	assert.Equal(t, generated.SearchResultMatchSecretName, resp.Results[1].Match)
	assert.Equal(t, generated.SearchResultKindSecret, resp.Results[1].Kind)
	assert.Equal(t, "api-app-secret", resp.Results[1].Label)
}

func newSecretsStore(t *testing.T, secrets []secretmodel.Secret) *secretstore.FileStore {
	t.Helper()
	ctx := context.Background()
	store, err := secretstore.NewFileStore(ctx, t.TempDir()+"/secrets.state.json", fs.NewLocalFileSystem())
	require.NoError(t, err)
	require.NoError(t, store.Replace(ctx, secrets))
	return store
}

func newServiceStore(t *testing.T) *service.Store {
	t.Helper()

	store, err := service.NewStore(context.Background(), t.TempDir()+"/services.json", fs.NewLocalFileSystem())
	require.NoError(t, err)
	return store
}
