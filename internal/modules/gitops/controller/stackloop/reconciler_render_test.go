package stackloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestAddManagedLabel(t *testing.T) {
	tests := []struct {
		name            string
		labels          compose.Labels
		expectedMutated bool
	}{
		{
			name:            "labels absent",
			expectedMutated: true,
		},
		{
			name:            "managed label absent",
			labels:          *compose.NewLabels(map[string]string{"purpose": "api"}),
			expectedMutated: true,
		},
		{
			name: "managed label has incorrect value",
			labels: *compose.NewLabels(map[string]string{
				labelsdict.ServiceManagedLabelKey: "false",
			}),
			expectedMutated: true,
		},
		{
			name: "managed label already correct",
			labels: *compose.NewLabels(map[string]string{
				labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue,
			}),
			expectedMutated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := &pipelinePayload{Desired: &compose.File{Compose: compose.Compose{
				Services: compose.Services{{Name: "api", Deploy: compose.ServiceDeploy{Labels: tt.labels}}},
			}}}

			err := (&Reconciler{}).addManagedLabel(t.Context(), payload)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedMutated, payload.DesiredMutated)
			assert.Equal(
				t,
				labelsdict.ServiceManagedLabelValue,
				payload.Desired.Compose.Services[0].Deploy.Labels.Map[labelsdict.ServiceManagedLabelKey],
			)
			if tt.labels.Map["purpose"] != "" {
				assert.Equal(t, "api", payload.Desired.Compose.Services[0].Deploy.Labels.Map["purpose"])
			}
		})
	}
}

func TestStackDeployComposeOmitsInitJobsWithoutMutatingDesired(t *testing.T) {
	desired := &compose.File{
		Path: "/repo/app.yaml",
		Compose: compose.Compose{
			Services: compose.Services{
				{
					Name:  "api",
					Image: "nginx:latest",
					InitJobs: []compose.InitJob{
						{
							Name:       "migrate",
							Image:      "oryd/kratos:v1.3.1",
							Entrypoint: []string{"sh", "-ec", "exec kratos migrate sql --dsn \"$DSN\" --yes"},
						},
					},
				},
			},
			Secrets: compose.Secrets{
				"db-dsn": {
					Name: "db-dsn",
					File: "./secrets/db-dsn",
				},
			},
		},
	}

	rendered := stackDeployCompose(desired, "app")
	renderedRaw, err := rendered.MarshalYAML()
	require.NoError(t, err, "marshal rendered stack compose")

	assert.NotContains(t, string(renderedRaw), "x-init-deploy-jobs")
	assert.NotContains(t, string(renderedRaw), "$DSN")
	assert.Contains(t, string(renderedRaw), "db-dsn")

	require.Len(t, desired.Compose.Services[0].InitJobs, 1, "desired state must retain init jobs")
	assert.Contains(t, desired.Compose.Services[0].InitJobs[0].Entrypoint[2], "$DSN")
	assert.Equal(t, "/repo/app.yaml", desired.Path, "desired source path must remain unchanged")

	rendered.Compose.Secrets["db-dsn"].File = "/tmp/rendered-secret"
	assert.Equal(
		t,
		"./secrets/db-dsn",
		desired.Compose.Secrets["db-dsn"].File,
		"render normalization must not mutate desired shared objects",
	)
}

func TestNeedsRenderedCompose(t *testing.T) {
	t.Run("age without rotation or other mutations", func(t *testing.T) {
		assert.True(t, needsRenderedCompose(&pipelinePayload{Desired: &compose.File{Compose: compose.Compose{
			Secrets: compose.Secrets{"password": {File: "./secrets/password.age"}},
		}}}))
	})
	t.Run("desired mutated", func(t *testing.T) {
		assert.True(t, needsRenderedCompose(&pipelinePayload{
			DesiredMutated: true,
			Desired: &compose.File{
				Compose: compose.Compose{
					Services: compose.Services{{Name: "api"}},
				},
			},
		}))
	})

	t.Run("init job", func(t *testing.T) {
		assert.True(t, needsRenderedCompose(&pipelinePayload{
			Desired: &compose.File{
				Compose: compose.Compose{
					Services: compose.Services{{
						Name:     "api",
						InitJobs: []compose.InitJob{{Name: "migrate", Image: "example/migrate:latest"}},
					}},
				},
			},
		}))
	})

	t.Run("plain compose", func(t *testing.T) {
		assert.False(t, needsRenderedCompose(&pipelinePayload{
			Desired: &compose.File{
				Compose: compose.Compose{
					Services: compose.Services{{Name: "api"}},
				},
			},
		}))
	})
}

func TestStackDeployComposeExternalizesAllAPIManagedSecrets(t *testing.T) {
	desired := &compose.File{Compose: compose.Compose{Secrets: compose.Secrets{
		"password": {File: "./secrets/password.age", Labels: *compose.NewLabels(map[string]string{"purpose": "app"})},
		"rotated":  {Name: "app-rotated-abc123", File: "/repo/token.age"},
		"plain":    {File: "./secrets/plain.txt"},
		"external": {Name: "existing.age", External: true},
		"driver":   {Name: "vault-password", Driver: "vault", DriverOpts: map[string]string{"key": "app/db"}},
	}}}
	rendered := stackDeployCompose(desired, "app")
	assert.Equal(t, &compose.Secret{Name: "app_password", External: true}, rendered.Compose.Secrets["password"])
	assert.Equal(t, &compose.Secret{Name: "app-rotated-abc123", External: true}, rendered.Compose.Secrets["rotated"])
	assert.Equal(t, &compose.Secret{Name: "app_plain", External: true}, rendered.Compose.Secrets["plain"])
	assert.Equal(t, desired.Compose.Secrets["external"], rendered.Compose.Secrets["external"])
	assert.Equal(t, &compose.Secret{Name: "vault-password", External: true}, rendered.Compose.Secrets["driver"])
	assert.Equal(t, "vault", desired.Compose.Secrets["driver"].Driver)
	assert.Equal(t, "./secrets/password.age", desired.Compose.Secrets["password"].File)
	assert.False(t, desired.Compose.Secrets["password"].External)
	assert.Equal(t, "app", desired.Compose.Secrets["password"].Labels.Map["purpose"])
	raw, err := rendered.MarshalYAML()
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "password.age")
	assert.NotContains(t, string(raw), "/repo/token.age")
	assert.Contains(t, string(raw), "external: true")
}
