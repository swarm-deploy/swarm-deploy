package compose

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalStackYAMLOmitsInitJobsWithoutMutatingSource(t *testing.T) {
	file := &File{
		Compose: Compose{
			Services: Services{
				{
					Name:  "api",
					Image: "nginx:latest",
					InitJobs: []InitJob{
						{
							Name:       "migrate",
							Image:      "oryd/kratos:v1.3.1",
							Entrypoint: []string{"sh", "-ec", "exec kratos migrate sql --dsn \"$DSN\" --yes"},
						},
					},
				},
			},
			Secrets: SharedObjects{
				"db-dsn": {
					Name: "db-dsn",
				},
			},
		},
	}

	rendered, err := file.MarshalStackYAML()
	require.NoError(t, err, "marshal stack yaml")

	renderedString := string(rendered)
	assert.NotContains(t, renderedString, "x-init-deploy-jobs", "stack compose must not include init jobs")
	assert.NotContains(t, renderedString, "$DSN", "init-job shell content must not reach docker stack deploy")
	assert.Contains(t, renderedString, "db-dsn", "top-level referenced resources must remain available")

	require.Len(t, file.Compose.Services[0].InitJobs, 1, "source compose init jobs must remain intact")
	assert.True(
		t,
		strings.Contains(file.Compose.Services[0].InitJobs[0].Entrypoint[2], "$DSN"),
		"source init-job command must preserve normal shell variable syntax",
	)
}
