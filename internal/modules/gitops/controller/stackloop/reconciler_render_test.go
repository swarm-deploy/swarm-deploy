package stackloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"go.uber.org/mock/gomock"
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
			Secrets: compose.SharedObjects{
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


func TestDeployStackRendersInitJobsWhenSourceComposeWouldBeUsed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)

	dataDir := t.TempDir()
	sourcePath := filepath.Join(dataDir, "repo", "app.yaml")
	renderedPath := filepath.Join(dataDir, "rendered", "app.yaml")

	repository.EXPECT().WorkingDir().Return(filepath.Dir(sourcePath))
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", renderedPath, gomock.Any()).
		Return(nil)

	desired := &compose.File{
		Path: sourcePath,
		Compose: compose.Compose{
			Services: compose.Services{
				{
					Name:  "api",
					Image: "nginx:latest",
					InitJobs: []compose.InitJob{
						{
							Name:       "migrate",
							Image:      "oryd/kratos:v1.3.1",
							Entrypoint: []string{"sh", "-ec", "echo \"$DSN\""},
						},
					},
				},
			},
		},
	}

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: dataDir,
			},
		},
		git:      repository,
		deployer: stackDeployer,
	}

	payload := &pipelinePayload{
		Stack: config.StackSpec{
			Name: "app",
		},
		Desired:           desired,
		DeployComposePath: sourcePath,
	}

	err := reconciler.deployStack(context.Background(), payload)
	require.NoError(t, err, "deploy stack")

	renderedRaw, err := os.ReadFile(renderedPath)
	require.NoError(t, err, "read rendered compose")
	assert.NotContains(t, string(renderedRaw), "x-init-deploy-jobs")
	assert.NotContains(t, string(renderedRaw), "$DSN")
	assert.Equal(t, sourcePath, desired.Path, "desired path must remain unchanged")
	require.Len(t, desired.Compose.Services[0].InitJobs, 1, "desired state must retain init jobs")
}
