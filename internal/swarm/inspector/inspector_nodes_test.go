package inspector

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

func TestToNodeInfoMapsFields(t *testing.T) {
	node := dockerswarm.Node{
		ID: " node-1 ",
		Description: dockerswarm.NodeDescription{
			Hostname: " manager-1 ",
			Engine: dockerswarm.EngineDescription{
				EngineVersion: " 28.3.0 ",
			},
		},
		Status: dockerswarm.NodeStatus{
			State: dockerswarm.NodeStateReady,
			Addr:  " 10.0.0.1 ",
		},
		Spec: dockerswarm.NodeSpec{
			Availability: dockerswarm.NodeAvailabilityActive,
		},
		ManagerStatus: &dockerswarm.ManagerStatus{
			Leader: true,
		},
	}

	mapped := toNodeInfo(node)

	assert.Equal(t, "node-1", mapped.ID, "unexpected id")
	assert.Equal(t, "manager-1", mapped.Hostname, "unexpected hostname")
	assert.Equal(t, "ready", mapped.Status, "unexpected status")
	assert.Equal(t, "active", mapped.Availability, "unexpected availability")
	assert.Equal(t, "leader", mapped.ManagerStatus, "unexpected managerStatus")
	assert.Equal(t, "28.3.0", mapped.EngineVersion, "unexpected engine version")
	assert.Equal(t, "10.0.0.1", mapped.Addr, "unexpected addr")
}

func TestToNodeInfoSetsWorkerManagerStatusForWorkers(t *testing.T) {
	node := dockerswarm.Node{
		ID: "node-2",
	}

	mapped := toNodeInfo(node)

	assert.Equal(t, "worker", mapped.ManagerStatus, "worker node must have worker managerStatus")
}

func TestToNodeInfoUsesReachabilityForManagers(t *testing.T) {
	node := dockerswarm.Node{
		ID: "node-3",
		ManagerStatus: &dockerswarm.ManagerStatus{
			Reachability: dockerswarm.ReachabilityReachable,
		},
	}

	mapped := toNodeInfo(node)

	assert.Equal(t, "reachable", mapped.ManagerStatus, "manager must use reachability")
}
