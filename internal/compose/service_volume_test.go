package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
