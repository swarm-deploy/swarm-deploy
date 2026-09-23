package srvcomparator

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestPortComparatorComparePorts(t *testing.T) {
	testCases := []struct {
		name     string
		left     compose.ServicePorts
		right    compose.ServicePorts
		expected []diff.PortDiff
	}{
		{
			name: "detects added removed and changed ports",
			left: compose.ServicePorts{Ports: []compose.ServicePort{
				{Published: 80, Target: 8080, Protocol: dockerswarm.PortConfigProtocolTCP, Mode: dockerswarm.PortConfigPublishModeIngress},
				{Published: 90, Target: 9090, Protocol: dockerswarm.PortConfigProtocolTCP, Mode: dockerswarm.PortConfigPublishModeIngress},
			}},
			right: compose.ServicePorts{Ports: []compose.ServicePort{
				{Published: 80, Target: 8081, Protocol: dockerswarm.PortConfigProtocolTCP, Mode: dockerswarm.PortConfigPublishModeIngress},
				{Published: 443, Target: 8443, Protocol: dockerswarm.PortConfigProtocolTCP, Mode: dockerswarm.PortConfigPublishModeHost},
			}},
			expected: []diff.PortDiff{
				{Published: 80, Target: 8080, Protocol: "tcp", Mode: "ingress", Removed: true},
				{Published: 80, Target: 8081, Protocol: "tcp", Mode: "ingress", Added: true},
				{Published: 90, Target: 9090, Protocol: "tcp", Mode: "ingress", Removed: true},
				{Published: 443, Target: 8443, Protocol: "tcp", Mode: "host", Added: true},
			},
		},
		{
			name: "returns empty for equal ports",
			left: compose.ServicePorts{Ports: []compose.ServicePort{
				{Published: 80, Target: 8080, Protocol: dockerswarm.PortConfigProtocolTCP, Mode: dockerswarm.PortConfigPublishModeIngress},
			}},
			right: compose.ServicePorts{Ports: []compose.ServicePort{
				{Published: 80, Target: 8080, Protocol: dockerswarm.PortConfigProtocolTCP, Mode: dockerswarm.PortConfigPublishModeIngress},
			}},
			expected: []diff.PortDiff{},
		},
	}

	comparator := &PortComparator{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := comparator.ComparePorts(testCase.left, testCase.right)

			assert.Equal(t, testCase.expected, actual, "unexpected port changes")
		})
	}
}
