package compose

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestServicePorts_parseStringView(t *testing.T) {
	tests := []struct {
		Input    string
		Expected *ServicePort
	}{
		{
			Input: "4321:1234",
			Expected: &ServicePort{
				Published: 4321,
				Target:    1234,
				Protocol:  dockerswarm.PortConfigProtocolTCP,
			},
		},
		{
			Input: "4321:1234/udp",
			Expected: &ServicePort{
				Published: 4321,
				Target:    1234,
				Protocol:  dockerswarm.PortConfigProtocolUDP,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Input, func(t *testing.T) {
			sp := ServicePorts{}

			got, err := sp.parseStringView(test.Input)
			require.NoError(t, err)

			assert.Equal(t, test.Expected, got)
		})
	}
}

func TestServicePorts_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		Title    string
		Input    string
		Expected ServicePorts
	}{
		{
			Title: "sequence",
			Input: `
- target: 80
  published: 80
  protocol: tcp
  mode: host
`,
			Expected: ServicePorts{
				Ports: []ServicePort{
					{
						Target:    80,
						Published: 80,
						Protocol:  dockerswarm.PortConfigProtocolTCP,
						Mode:      "host",
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Title, func(t *testing.T) {
			var sp ServicePorts

			err := yaml.Unmarshal([]byte(test.Input), &sp)
			require.NoError(t, err)

			assert.Equal(t, test.Expected, sp)
		})
	}
}
