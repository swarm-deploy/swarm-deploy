package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/webroute"
)

type fakeSecretsReader struct {
	list []swarm.Secret
	err  error
}

func (f fakeSecretsReader) List(_ context.Context) ([]swarm.Secret, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.list, nil
}

func TestHandlerGetSecretByName(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	h := &handler{
		secrets: fakeSecretsReader{
			list: []swarm.Secret{
				{
					ID:        "secret-id",
					Name:      "db-password",
					VersionID: 7,
					CreatedAt: now.Add(-time.Hour),
					UpdatedAt: now,
					Driver:    "vault",
					Labels: map[string]string{
						"external_path":       "kv/prod/db-password",
						"external_version_id": "v12",
						"scope":               "production",
					},
				},
			},
		},
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

	h := &handler{
		secrets: fakeSecretsReader{
			list: []swarm.Secret{{Name: "known-secret"}},
		},
	}

	_, err := h.GetSecretByName(context.Background(), generated.GetSecretByNameParams{Name: "unknown-secret"})
	require.Error(t, err)

	var sErr *statusError
	require.True(t, errors.As(err, &sErr))
	assert.Equal(t, 404, sErr.code)
}

func TestHandlerSearch_PriorityAndDedupe(t *testing.T) {
	t.Parallel()

	servicesStore, err := service.NewStore(t.TempDir() + "/services.json")
	require.NoError(t, err)
	require.NoError(t, servicesStore.ReplaceStack("payments", []service.Info{
		{
			Name: "api-app",
			Type: "application",
			WebRoutes: []webroute.Route{
				{Domain: "api-app.example.com", Address: "10.10.0.5", Port: "443"},
			},
		},
		{
			Name: "billing",
			Type: "application",
			WebRoutes: []webroute.Route{
				{Domain: "billing.example.com", Address: "10.10.0.7", Port: "443"},
			},
		},
	}))

	h := &handler{
		services: servicesStore,
		secrets: fakeSecretsReader{
			list: []swarm.Secret{
				{Name: "api-app-secret"},
			},
		},
		networks: fakeNetworksReader{
			list: []swarm.Network{
				{Name: "billing-internal"},
			},
		},
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

	resp, err = h.Search(context.Background(), generated.SearchParams{Query: "internal"})
	require.NoError(t, err)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, generated.SearchResultKindStack, resp.Results[0].Kind)
	assert.Equal(t, generated.SearchResultMatchStackName, resp.Results[0].Match)
	assert.Equal(t, "billing-internal", resp.Results[0].Label)
}
