package recommendations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestInitServiceRegistersBuiltInAnalyzers(t *testing.T) {
	ctx := context.Background()
	service, err := InitService(ctx, filepath.Join(t.TempDir(), "recommendations.json"), fs.NewLocalFileSystem())
	require.NoError(t, err)

	stack := model.Stack{
		Name: "payments",
		Definition: compose.File{
			Compose: compose.Compose{
				Services: compose.Services{
					{
						Name:   "api",
						Image:  "api@sha256:" + strings.Repeat("a", 64),
						CapAdd: []string{"ALL"},
						Deploy: compose.ServiceDeploy{
							Resources: &compose.ServiceDeployResources{
								Limits: &compose.ServiceDeployResource{Memory: "128M"},
							},
						},
						Volumes: compose.ServiceVolumes{
							Volumes: []*compose.ServiceVolume{
								{
									Type:   compose.ServiceVolumeTypeBind,
									Source: "/var/run/docker.sock",
									Target: "/var/run/docker.sock",
								},
							},
						},
					},
				},
			},
		},
	}

	require.NoError(t, service.Recommender.Recommend(ctx, stack))
	recommendations, err := service.Store.List(ctx, modelstore.ListFilter{Stack: stack.Name})
	require.NoError(t, err)
	require.Len(t, recommendations, 3)

	assert.Equal(t, model.TypeServiceRestartPolicyUnspecified, recommendations[0].Type)
	assert.Equal(t, model.TypeServiceDockerSocketMount, recommendations[1].Type)
	assert.Equal(t, model.TypeServiceCapabilitiesAll, recommendations[2].Type)
}
