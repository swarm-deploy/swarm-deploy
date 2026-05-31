package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
				Target:   "/var/log/nginx",
				Bind: &ServiceVolumeBind{
					Propagation: "rslave",
				},
			},
			Expected: "/var/log/nginx:/var/log/nginx:ro,rslave",
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			got := test.Input.MarshalString()
			assert.Equal(t, test.Expected, got)
		})
	}
}
