package compose

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
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
				Mode:      dockerswarm.PortConfigPublishModeIngress,
			},
		},
		{
			Input: "4321:1234/udp",
			Expected: &ServicePort{
				Published: 4321,
				Target:    1234,
				Protocol:  dockerswarm.PortConfigProtocolUDP,
				Mode:      dockerswarm.PortConfigPublishModeIngress,
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

func TestPortRanges(t *testing.T) {
	for _, tc := range []struct {
		source string
		count  int
		bad    bool
	}{
		{"8000-8002:9000-9002/udp", 3, false},
		{"9000-9002", 3, false},
		{"8000-8001:9000-9002", 0, true},
		{"8002-8000:9000-9002", 0, true},
		{"8000-8002:9000-65536", 0, true},
	} {
		t.Run(tc.source, func(t *testing.T) {
			var ports ServicePorts
			err := yaml.Unmarshal([]byte("['"+tc.source+"']"), &ports)
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, ports.Ports, tc.count)
			assert.Equal(t, 9000, ports.Ports[0].Target)
			assert.Equal(t, 9002, ports.Ports[2].Target)
			rendered, err := yaml.Marshal(ports)
			require.NoError(t, err)
			var again ServicePorts
			require.NoError(t, yaml.Unmarshal(rendered, &again))
			assert.Equal(t, ports.Ports, again.Ports)
		})
	}
}
