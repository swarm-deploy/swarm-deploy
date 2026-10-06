package stackloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

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

	rendered := stackDeployCompose(desired)
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
