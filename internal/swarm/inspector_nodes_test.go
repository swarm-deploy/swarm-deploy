package swarm

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

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
