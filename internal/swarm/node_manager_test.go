package swarm

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

func TestNodeManagerMapNodeMapsFields(t *testing.T) {
	node := dockerswarm.Node{
		ID: " node-1 ",
		Description: dockerswarm.NodeDescription{
			Hostname: " manager-1 ",
			Engine: dockerswarm.EngineDescription{
				EngineVersion: " 28.3.0 ",
			},
			Resources: dockerswarm.Resources{
				NanoCPUs:    8_000_000_000,
				MemoryBytes: 34_359_738_368,
			},
		},
		Status: dockerswarm.NodeStatus{
			State: dockerswarm.NodeStateReady,
			Addr:  " 10.0.0.1 ",
		},
		Spec: dockerswarm.NodeSpec{
			Availability: dockerswarm.NodeAvailabilityActive,
			Annotations: dockerswarm.Annotations{
				Labels: map[string]string{
					"role": "manager",
					"zone": "eu-1",
				},
			},
		},
		ManagerStatus: &dockerswarm.ManagerStatus{
			Leader: true,
		},
	}

	mapped := (&NodeManager{}).mapNode(node)

	assert.Equal(t, " node-1 ", mapped.ID, "unexpected id")
	assert.Equal(t, " manager-1 ", mapped.Hostname, "unexpected hostname")
	assert.Equal(t, "ready", mapped.Status, "unexpected status")
	assert.Equal(t, "active", mapped.Availability, "unexpected availability")
	assert.Equal(t, NodeManagerStatusLeader, mapped.ManagerStatus, "unexpected managerStatus")
	assert.Equal(t, " 28.3.0 ", mapped.EngineVersion, "unexpected engine version")
	assert.Equal(t, " 10.0.0.1 ", mapped.Addr, "unexpected addr")
	assert.Equal(t, int64(8_000_000_000), mapped.CPUNano, "unexpected cpu")
	assert.Equal(t, int64(34_359_738_368), mapped.MemoryBytes, "unexpected memory")
	assert.Equal(t, map[string]string{"role": "manager", "zone": "eu-1"}, mapped.Labels, "unexpected labels")
}

func TestNodeManagerMapNodeSetsWorkerManagerStatusForWorkers(t *testing.T) {
	node := dockerswarm.Node{
		ID: "node-2",
	}

	mapped := (&NodeManager{}).mapNode(node)

	assert.Equal(t, NodeManagerStatusWorker, mapped.ManagerStatus, "worker node must have worker managerStatus")
	assert.Nil(t, mapped.Labels, "worker node without labels must keep labels nil")
}
