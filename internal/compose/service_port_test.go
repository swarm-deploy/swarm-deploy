package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServicePorts_parseStringView(t *testing.T) {
	tests := []struct {
		Input    string
		Expected *ServicePort
	}{
		{
			Input: "4321:1234",
			Expected: &ServicePort{
				Published: "4321",
				Target:    1234,
				Protocol:  PortProtocolTCP,
			},
		},
		{
			Input: "4321:1234/udp",
			Expected: &ServicePort{
				Published: "4321",
				Target:    1234,
				Protocol:  PortProtocolUDP,
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
