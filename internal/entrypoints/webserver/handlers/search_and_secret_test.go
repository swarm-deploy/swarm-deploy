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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
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
	h := &handler{
		secrets: secretsReader,
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
}

func TestHandlerGetSecretByName_NotFound(t *testing.T) {
	t.Parallel()

	secretsReader := newSecretsStore(t, []secretmodel.Secret{{Name: "known-secret"}})
	h := &handler{
		secrets: secretsReader,
	}

	_, err := h.GetSecretByName(context.Background(), generated.GetSecretByNameParams{Name: "unknown-secret"})
	require.Error(t, err)

	var sErr *statusError
	require.True(t, errors.As(err, &sErr))
	assert.Equal(t, 404, sErr.code)
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
