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
				Target: "/var/log/nginx",
			},
			Expected: "/var/log/nginx",
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
						Type:   "bind",
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
