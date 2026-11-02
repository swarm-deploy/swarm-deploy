package stack

import (
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestSingleServiceComposeVolumeDefaultsMatchFullConversion(t *testing.T) {
	for _, tc := range []struct {
		name, source, kind string
		wantVolume         bool
		wantType           mount.Type
	}{
		{name: "implicit named volume", source: "data", wantVolume: true, wantType: mount.TypeVolume},
		{name: "explicit named volume", source: "data", kind: "volume", wantVolume: true, wantType: mount.TypeVolume},
		{name: "explicit bind", source: "/host/data", kind: "bind", wantType: mount.TypeBind},
		{name: "anonymous volume", wantType: mount.TypeVolume},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind := ""
			if tc.kind != "" {
				kind = "        type: " + tc.kind + "\n"
			}
			desired, err := compose.Parse([]byte(fmt.Sprintf(`services:
  app:
    image: nginx:alpine
    volumes:
      - source: %q
        target: /data
%svolumes:
  data: {}
  unused: {}
  /host/data: {}
`, tc.source, kind)))
			require.NoError(t, err)
			ctrl := gomock.NewController(t)
			networks := swarm.NewMockNetworkManager(ctrl)
			networks.EXPECT().Get(gomock.Any(), "app_default").Return(swarm.Network{ID: "network", Name: "app_default", Scope: "swarm", Driver: "overlay"}, nil).AnyTimes()
			convert := converter.New(networks, swarm.NewMockConfigManager(ctrl), swarm.NewMockSecretManager(ctrl))
			full, err := convert.Convert(t.Context(), "app", *desired)
			require.NoError(t, err, "full deployment must accept the mount")
			selected, err := SingleServiceCompose(*desired, "app")
			require.NoError(t, err)
			if tc.wantVolume {
				assert.Equal(t, compose.Volumes{"data": desired.Volumes["data"]}, selected.Volumes)
			} else {
				assert.Empty(t, selected.Volumes)
			}
			single, err := convert.Convert(t.Context(), "app", selected)
			require.NoError(t, err, "single-service self-heal must accept the same mount")
			assert.Equal(t, full.Services, single.Services)
			require.Len(t, single.Services, 1)
			mounts := single.Services[0].TaskTemplate.ContainerSpec.Mounts
			require.Len(t, mounts, 1)
			assert.Equal(t, tc.wantType, mounts[0].Type)
			assert.NotContains(t, selected.Volumes, "unused")
		})
	}
}
