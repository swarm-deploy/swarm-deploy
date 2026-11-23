package preparation

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestParsedMountsSurvivePreparationConversionAndServiceUpdate(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, definition, source string
		kind                                  mount.Type
	}{
		{"dotted", "db.data:/data", "db.data:", "demo_db.data", mount.TypeVolume},
		{"hyphenated", "db-data:/data", "db-data:", "demo_db-data", mount.TypeVolume},
		{"underscored", "db_data:/data", "db_data:", "demo_db_data", mount.TypeVolume},
		{"relative", "./data:/data", "{}", "/repo/deploy/data", mount.TypeBind},
		{"parent", "../data:/data", "{}", "/repo/data", mount.TypeBind},
		{"absolute", "/var/data:/data", "{}", "/var/data", mount.TypeBind},
		{"external alias", "db.data:/data", "db.data: {external: true}", "db.data", mount.TypeVolume},
		{"external name", "db.data:/data", "db.data: {external: true, name: existing.data}", "existing.data", mount.TypeVolume},
		{"explicit name", "db.data:/data", "db.data: {name: existing.data}", "existing.data", mount.TypeVolume},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mountOptions := ":ro,cached,nocopy"
			if tc.kind == mount.TypeBind {
				mountOptions = ":ro,cached,rslave"
			}
			for _, options := range []string{"", mountOptions} {
				t.Run(options, func(t *testing.T) {
					parsed, err := compose.Parse([]byte(fmt.Sprintf("services:\n  db:\n    image: busybox:1.36\n    volumes: [%q]\nvolumes:\n  %s\n", tc.declaration+options, tc.definition)))
					require.NoError(t, err)
					normalizeBindMountPaths(filepath.FromSlash("/repo/deploy"), parsed.Services)
					ctrl := gomock.NewController(t)
					networks := swarm.NewMockNetworkManager(ctrl)
					networks.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{ID: "network-id", Name: "demo_default", Scope: "swarm"}, nil)
					converted, err := converter.New(networks, swarm.NewMockConfigManager(ctrl), swarm.NewMockSecretManager(ctrl)).Convert(t.Context(), "demo", *parsed)
					require.NoError(t, err)
					require.Len(t, converted.Services, 1)
					desired := converted.Services[0]
					mounts := desired.TaskTemplate.ContainerSpec.Mounts
					require.Len(t, mounts, 1)
					assert.Equal(t, tc.kind, mounts[0].Type)
					assert.Equal(t, tc.source, mounts[0].Source)
					assert.Equal(t, "/data", mounts[0].Target)
					assert.Equal(t, options != "", mounts[0].ReadOnly)
					if options != "" {
						assert.Equal(t, mount.ConsistencyCached, mounts[0].Consistency)
						if tc.kind == mount.TypeVolume {
							require.NotNil(t, mounts[0].VolumeOptions)
							assert.True(t, mounts[0].VolumeOptions.NoCopy)
						} else {
							require.NotNil(t, mounts[0].BindOptions)
							assert.Equal(t, mount.PropagationRSlave, mounts[0].BindOptions.Propagation)
						}
					}
					// Updating the image must retain the existing volume identity.
					current := desired
					container := *current.TaskTemplate.ContainerSpec
					container.Image = "busybox:old"
					current.TaskTemplate.ContainerSpec = &container
					manager := swarm.NewMockServiceManager(ctrl)
					manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(dockerswarm.Service{ID: "db-id", Spec: current}, nil)
					manager.EXPECT().Update(gomock.Any(), "db-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ any, _ string, _ dockerswarm.Version, actual dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
							assert.Equal(t, mounts, actual.TaskTemplate.ContainerSpec.Mounts)
							return nil
						})
					result, err := services.New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", converted.Services, services.ApplyOptions{})
					require.NoError(t, err)
					assert.Equal(t, []string{"db"}, result.ChangedServices)
				})
			}
		})
	}
}
