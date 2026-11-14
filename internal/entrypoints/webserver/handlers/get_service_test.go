package handlers

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretservice"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestHandlerGetService(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	require.NoError(t, store.ReplaceStack(ctx, "payments", []model.Info{
		{
			Name:  "api",
			Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
			Metadata: metadata.Metadata{Links: []metadata.Link{
				{Type: "Grafana", URL: "https://grafana.example.com/d/payments"}},
			},
			Spec: swarm.ServiceSpec{
				Image:             "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				Mode:              "replicated",
				Replicas:          2,
				RequestedRAMBytes: 268435456,
				RequestedCPUNano:  500000000,
				LimitRAMBytes:     536870912,
				LimitCPUNano:      1000000000,
				Labels: map[string]string{
					"com.docker.stack.namespace":      "payments",
					"org.swarm-deploy.service.type":   "application",
					"org.swarm-deploy.service.public": "true",
					"app.env":                         "prod",
				},
				Secrets: []swarm.ServiceSecret{
					{
						SecretName: "payments_db_password",
						Target:     "/run/secrets/payments_db_password",
					},
				},
				Network: []swarm.ServiceNetwork{
					{
						Target:  "payments_default",
						Aliases: []string{"api"},
					},
				},
			},
		},
	}))
	secretsStore := newSecretsStore(t, []secretmodel.Secret{{
		ID: "secret-id", Name: "payments_db_password", VersionID: 3,
	}})

	h := &handler{
		services:        store,
		secretRelations: secretservice.NewResolver(store, secretsStore),
	}

	resp, err := h.GetService(context.Background(), generated.GetServiceParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "payments", resp.Stack)
	assert.Equal(t, "api", resp.Service)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", resp.Spec.Image)
	assert.Equal(t, "replicated", resp.Spec.Mode)
	require.Len(t, resp.Links, 1)
	assert.Equal(t, generated.ServiceLink{Type: "Grafana", URL: "https://grafana.example.com/d/payments"}, resp.Links[0])
	assert.Equal(t, int64(2), resp.Spec.Replicas)
	assert.EqualValues(t, 268435456, resp.Spec.RequestedRAMBytes)
	assert.EqualValues(t, 500000000, resp.Spec.RequestedCPUNano)
	assert.EqualValues(t, 536870912, resp.Spec.LimitRAMBytes)
	assert.EqualValues(t, 1000000000, resp.Spec.LimitCPUNano)
	labels, ok := resp.Spec.Labels.Get()
	require.True(t, ok)
	dockerLabels, ok := labels.Docker.Get()
	require.True(t, ok)
	assert.Equal(t, generated.ServiceSpecLabelGroupResponse{
		"com.docker.stack.namespace": "payments",
	}, dockerLabels)
	swarmDeployLabels, ok := labels.SwarmDeploy.Get()
	require.True(t, ok)
	assert.Equal(t, generated.ServiceSpecLabelGroupResponse{
		"org.swarm-deploy.service.type":   "application",
		"org.swarm-deploy.service.public": "true",
	}, swarmDeployLabels)
	customLabels, ok := labels.Custom.Get()
	require.True(t, ok)
	assert.Equal(t, generated.ServiceSpecLabelGroupResponse{
		"app.env": "prod",
	}, customLabels)
	require.Len(t, resp.Spec.Secrets, 1)
	assert.Equal(t, "payments_db_password", resp.Spec.Secrets[0].SecretName)
	assert.Equal(t, "/run/secrets/payments_db_password", resp.Spec.Secrets[0].Target.Value)
	secret, ok := resp.Spec.Secrets[0].Secret.Get()
	require.True(t, ok)
	assert.Equal(t, "secret-id", secret.ID)
	assert.Equal(t, int64(3), secret.VersionID)
	require.Len(t, resp.Spec.Network, 1)
	assert.Equal(t, "payments_default", resp.Spec.Network[0].Target)
}

func TestHandlerGetService_NotFound(t *testing.T) {
	t.Parallel()

	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))

	h := &handler{
		services: store,
	}

	_, err := h.GetService(context.Background(), generated.GetServiceParams{
		Stack:   "payments",
		Service: "api",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, http.StatusNotFound, statusErr.code)
}

func TestHandlerGetServiceReturnsRawSecretReferencesWhenResolutionFails(t *testing.T) {
	t.Parallel()

	services := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	reference := swarm.ServiceSecret{
		SecretID:   "secret-id",
		SecretName: "database-password",
		Target:     "/run/secrets/database-password",
	}
	require.NoError(t, services.ReplaceStack(t.Context(), "payments", []model.Info{{
		Name: "api",
		Spec: swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{reference}},
	}}))

	secretDB, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	secrets := secretstore.NewSQLStore(secretDB)
	require.NoError(t, secretDB.Close())

	h := &handler{
		services:        services,
		secretRelations: secretservice.NewResolver(services, secrets),
	}

	resp, err := h.GetService(t.Context(), generated.GetServiceParams{Stack: "payments", Service: "api"})
	require.NoError(t, err)
	require.Len(t, resp.Spec.Secrets, 1)
	assert.Equal(t, reference.SecretID, resp.Spec.Secrets[0].SecretID.Value)
	assert.Equal(t, reference.SecretName, resp.Spec.Secrets[0].SecretName)
	assert.Equal(t, reference.Target, resp.Spec.Secrets[0].Target.Value)
	assert.False(t, resp.Spec.Secrets[0].Secret.IsSet())
}
