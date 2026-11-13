package preparation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestCheckImagePolicyAggregatesIssuesInOrder(t *testing.T) {
	err := checkImagePolicy(context.Background(), config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}}, &compose.File{
		Path: "/repo/app.yaml",
		Compose: compose.Compose{Services: compose.Services{
			{Name: "api", Image: "nginx:latest", InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "example/migrate:latest"},
			}},
			{Name: "worker", Image: "registry.example/worker", InitJobs: []compose.InitJob{
				{Name: "seed", Image: "example/seed"},
			}},
			{Name: "valid", Image: "registry.example/valid:1.2.3", InitJobs: []compose.InitJob{
				{Name: "prepare", Image: "example/prepare:1.2.3"},
			}},
		}},
	})

	var validationErr *compose.ValidateComposeError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, "/repo/app.yaml", validationErr.FilePath)
	wantOrder := []struct{ service, field string }{
		{service: "api", field: "image"},
		{service: "api", field: `x-init-deploy-jobs["migrate"].image`},
		{service: "worker", field: "image"},
		{service: "worker", field: `x-init-deploy-jobs["seed"].image`},
	}
	require.Len(t, validationErr.Issues, len(wantOrder))
	for i, want := range wantOrder {
		assert.Equal(t, want.service, validationErr.Issues[i].ResourceName, "issue %d", i)
		assert.Equal(t, want.field, validationErr.Issues[i].Field, "issue %d", i)
	}
}

func TestImagePolicyErrorsArePermanent(t *testing.T) {
	err := checkImagePolicy(context.Background(), config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}}, &compose.File{
		Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "nginx:latest"}}},
	})
	require.Error(t, err)
	var temporary interface{ Temporary() bool }
	assert.False(t, errors.As(err, &temporary))
}
