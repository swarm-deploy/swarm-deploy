package preparation

import (
	"context"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/policies"
	"testing"
)

func TestCheckImagePolicyReturnsAllStructuredIssues(t *testing.T) {
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
	require.Len(t, validationErr.Issues, 4)
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "api", Field: "image", Code: policies.ImageViolationLatest,
		Message: `image must not use the latest tag`,
	}, validationErr.Issues[0])
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "api", Field: `x-init-deploy-jobs["migrate"].image`,
		Code:    policies.ImageViolationLatest,
		Message: `init job "migrate": image must not use the latest tag`,
	}, validationErr.Issues[1])
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "worker", Field: "image", Code: policies.ImageViolationLatest,
		Message: `image must not use the latest tag`,
	}, validationErr.Issues[2])
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "worker", Field: `x-init-deploy-jobs["seed"].image`,
		Code:    policies.ImageViolationLatest,
		Message: `init job "seed": image must not use the latest tag`,
	}, validationErr.Issues[3])
	assert.Contains(t, validationErr.Error(), `service "api": image:`)
	assert.Contains(t, validationErr.Error(), `service "api": x-init-deploy-jobs["migrate"].image:`)
	assert.Contains(t, validationErr.Error(), `service "worker": x-init-deploy-jobs["seed"].image:`)
}

func TestImagePolicyErrorsArePermanent(t *testing.T) {
	err := checkImagePolicy(context.Background(), config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}}, &compose.File{
		Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "nginx:latest"}}},
	})
	require.Error(t, err)
	var temporary interface{ Temporary() bool }
	assert.False(t, errors.As(err, &temporary))
}
