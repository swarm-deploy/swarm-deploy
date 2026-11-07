package compose

import (
	"os"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestServiceVolume_MarshalString(t *testing.T) {
	tests := []struct {
		Title    string
		Input    ServiceVolume
		Expected string
	}{
		{
			Title: "anonymous",
			Input: ServiceVolume{
				Target: "/var/log/nginx",
			},
			Expected: "/var/log/nginx",
		},
		{
			Title: "bind",
			Input: ServiceVolume{
				Source: "/var/log/nginx",
				Target: "/var/nginx",
			},
			Expected: "/var/log/nginx:/var/nginx",
		},
		{
			Title: "bind with readonly",
			Input: ServiceVolume{
				Source:   "/var/log/nginx",
				ReadOnly: true,
				Target:   "/var/log/nginx",
			},
			Expected: "/var/log/nginx:/var/log/nginx:ro",
		},
		{
			Title: "bind with readonly and rslave",
			Input: ServiceVolume{
				Source:   "/var/log/nginx",
				ReadOnly: true,
				Target:   "/var/nginx",
				Bind: &ServiceVolumeBind{
					Propagation: "rslave",
				},
			},
			Expected: "/var/log/nginx:/var/nginx:ro,rslave",
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			got := test.Input.MarshalString()
			assert.Equal(t, test.Expected, got)
		})
	}
}

func TestServiceVolume_UnmarshalString(t *testing.T) {
	tests := []struct {
		Title    string
		Input    string
		Expected ServiceVolume
	}{
		{
			Title: "bind",
			Input: "/var/log/nginx:/var/nginx",
			Expected: ServiceVolume{
				Type:     ServiceVolumeTypeBind,
				Source:   "/var/log/nginx",
				Target:   "/var/nginx",
				ReadOnly: false,
				isString: true,
			},
		},
		{
			Title: "bind readonly",
			Input: "/var/log/nginx:/var/nginx:ro",
			Expected: ServiceVolume{
				Type:     ServiceVolumeTypeBind,
				Source:   "/var/log/nginx",
				Target:   "/var/nginx",
				ReadOnly: true,
				isString: true,
			},
		},
		{
			Title: "bind readonly and rslave",
			Input: "/var/log/nginx:/var/nginx:ro,rslave",
			Expected: ServiceVolume{
				Type:     ServiceVolumeTypeBind,
				Source:   "/var/log/nginx",
				Target:   "/var/nginx",
				ReadOnly: true,
				Bind: &ServiceVolumeBind{
					Propagation: "rslave",
				},
				isString: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			sv := &ServiceVolume{}

			err := sv.UnmarshalString(test.Input)
			require.NoError(t, err)

			assert.Equal(t, test.Expected, *sv)
		})
	}
}

func TestServiceVolumesUnmarshalYAML(t *testing.T) {
	tests := []struct {
		Title    string
		Input    string
		Expected ServiceVolumes
	}{
		{
			Title: "only strings",
			Input: `
- /var/log/nginx:/var/nginx
`,
			Expected: ServiceVolumes{
				Volumes: []*ServiceVolume{
					{
						Type:     ServiceVolumeTypeBind,
						Source:   "/var/log/nginx",
						Target:   "/var/nginx",
						isString: true,
					},
				},
				Map: map[string]*ServiceVolume{
					"/var/nginx": {
						Type:     ServiceVolumeTypeBind,
						Source:   "/var/log/nginx",
						Target:   "/var/nginx",
						isString: true,
					},
				},
			},
		},
		{
			Title: "only bind",
			Input: `
- type: bind
  source: /mnt/host-data
  target: /data     
  bind:
    propagation: rslave
`,
			Expected: ServiceVolumes{
				Volumes: []*ServiceVolume{
					{
						Type:   ServiceVolumeTypeBind,
						Source: "/mnt/host-data",
						Target: "/data",
						Bind: &ServiceVolumeBind{
							Propagation: "rslave",
						},
					},
				},
				Map: map[string]*ServiceVolume{
					"/data": {
						Type:   ServiceVolumeTypeBind,
						Source: "/mnt/host-data",
						Target: "/data",
						Bind: &ServiceVolumeBind{
							Propagation: "rslave",
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			var got ServiceVolumes

			err := yaml.Unmarshal([]byte(test.Input), &got)
			require.NoError(t, err)

			assert.Equal(t, test.Expected, got)
		})
	}
}

func TestServiceVolumesPreserveConversionOptions(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		check       func(*testing.T, *ServiceVolume)
	}{
		{"tmpfs mapping", "- type: tmpfs\n  target: /tmp\n  tmpfs: {size: 64m, mode: 0700}", func(t *testing.T, v *ServiceVolume) {
			require.NotNil(t, v.Tmpfs)
			assert.Equal(t, "64m", v.Tmpfs.Size)
			assert.Equal(t, os.FileMode(0o700), v.Tmpfs.Mode)
		}},
		{"unknown short mode", "- data:/data:z", func(t *testing.T, v *ServiceVolume) { assert.Equal(t, []string{"z"}, v.UnsupportedModes) }},
		{"nocopy", "- data:/data:nocopy", func(t *testing.T, v *ServiceVolume) { require.NotNil(t, v.Volume); assert.True(t, v.Volume.Nocopy) }},
		{"consistency", "- /srv:/data:cached", func(t *testing.T, v *ServiceVolume) { assert.Equal(t, mount.ConsistencyCached, v.Consistency) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var volumes ServiceVolumes
			require.NoError(t, yaml.Unmarshal([]byte(tc.input), &volumes))
			require.Len(t, volumes.Volumes, 1)
			tc.check(t, volumes.Volumes[0])
		})
	}
}
