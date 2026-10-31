package stack

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestSingleServiceComposeKeepsOnlyReferencedObjects(t *testing.T) {
	desired := compose.Compose{
		Services: compose.Services{{
			Name: "api", Secrets: []compose.ObjectRef{{Source: "api-secret"}},
			Configs:  []compose.ObjectRef{{Source: "api-config"}},
			Networks: compose.NewServiceNetworks(&compose.ServiceNetwork{Alias: "backend"}),
			Volumes: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{
				{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/data"},
				{Type: compose.ServiceVolumeTypeVolume, Source: "shared", Target: "/shared"},
				{Type: compose.ServiceVolumeTypeBind, Source: "/host", Target: "/host"},
			}},
		}},
		Networks: map[string]compose.Network{"backend": {}, "frontend": {}},
		Volumes:  compose.Volumes{"data": {}, "shared": {Name: "external-data", External: true}, "unused": {}},
		Secrets: compose.Secrets{
			"api-secret": {Alias: "api-secret"}, "worker-secret": {Alias: "worker-secret"},
		},
		Configs: compose.Configs{
			"api-config": {Alias: "api-config"}, "worker-config": {Alias: "worker-config"},
		},
	}

	selected, err := SingleServiceCompose(desired, "api")
	require.NoError(t, err)
	assert.Equal(t, map[string]compose.Network{"backend": {}}, selected.Networks)
	assert.Equal(t, compose.Secrets{"api-secret": desired.Secrets["api-secret"]}, selected.Secrets)
	assert.Equal(t, compose.Configs{"api-config": desired.Configs["api-config"]}, selected.Configs)
	assert.Equal(t, compose.Volumes{"data": desired.Volumes["data"], "shared": desired.Volumes["shared"]}, selected.Volumes)
	assert.Equal(t, desired.Services[0], selected.Services[0])
}

func TestSingleServiceComposeSelectsServiceNetworks(t *testing.T) {
	internal := true
	tests := []struct {
		name     string
		service  compose.Service
		networks map[string]compose.Network
		want     map[string]compose.Network
	}{
		{
			name: "implicit external default network", service: compose.Service{Name: "api"},
			networks: map[string]compose.Network{"default": {Name: "shared-network", External: true}},
			want:     map[string]compose.Network{"default": {Name: "shared-network", External: true}},
		},
		{
			name: "implicit custom default network", service: compose.Service{Name: "api"},
			networks: map[string]compose.Network{
				"default": {Internal: &internal, Extra: map[string]interface{}{"driver": "overlay"}},
			},
			want: map[string]compose.Network{
				"default": {Internal: &internal, Extra: map[string]interface{}{"driver": "overlay"}},
			},
		},
		{
			name: "explicitly referenced network",
			service: compose.Service{Name: "api", Networks: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "backend"},
			)},
			networks: map[string]compose.Network{"backend": {Name: "shared-backend", External: true}},
			want:     map[string]compose.Network{"backend": {Name: "shared-backend", External: true}},
		},
		{
			name: "unrelated network definitions are omitted",
			service: compose.Service{Name: "api", Networks: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "backend"},
			)},
			networks: map[string]compose.Network{
				"backend": {}, "frontend": {}, "default": {Name: "unused-default"},
			},
			want: map[string]compose.Network{"backend": {}},
		},
		{
			name:    "service without network declarations and no configured default",
			service: compose.Service{Name: "api"}, networks: map[string]compose.Network{"frontend": {}},
		},
		{name: "compose without network definitions", service: compose.Service{Name: "api"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, err := SingleServiceCompose(compose.Compose{
				Services: compose.Services{tt.service}, Networks: tt.networks,
			}, "api")
			require.NoError(t, err)
			assert.Equal(t, tt.want, selected.Networks)
		})
	}
}

func TestSingleServicePreservesCredentialConfig(t *testing.T) {
	desired := compose.Compose{Services: compose.Services{{Name: "api", CredentialSpec: &compose.CredentialSpec{Config: "credential"}}}, Configs: compose.Configs{"credential": {Name: "rotated"}, "unused": {}}}
	selected, err := SingleServiceCompose(desired, "api")
	require.NoError(t, err)
	assert.Equal(t, compose.Configs{"credential": desired.Configs["credential"]}, selected.Configs)
}
