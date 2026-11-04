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
			Platform: dockerswarm.Platform{OS: "linux", Architecture: "amd64"},
			Engine: dockerswarm.EngineDescription{
				EngineVersion: " 28.3.0 ",
				Labels:        map[string]string{"storage": "ssd"},
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
			Role:         dockerswarm.NodeRoleManager,
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

	mapped := (&nodeManager{}).mapNode(node)

	assert.Equal(t, " node-1 ", mapped.ID, "unexpected id")
	assert.Equal(t, "manager", mapped.Role, "unexpected role")
	assert.Equal(t, " manager-1 ", mapped.Hostname, "unexpected hostname")
	assert.Equal(t, "linux", mapped.PlatformOS, "unexpected platform OS")
	assert.Equal(t, "amd64", mapped.PlatformArchitecture, "unexpected platform architecture")
	assert.Equal(t, "ready", mapped.Status, "unexpected status")
	assert.Equal(t, "active", mapped.Availability, "unexpected availability")
	assert.Equal(t, NodeManagerStatusLeader, mapped.ManagerStatus, "unexpected managerStatus")
	assert.Equal(t, " 28.3.0 ", mapped.EngineVersion, "unexpected engine version")
	assert.Equal(t, map[string]string{"storage": "ssd"}, mapped.EngineLabels, "unexpected engine labels")
	assert.Equal(t, " 10.0.0.1 ", mapped.Addr, "unexpected addr")
	assert.Equal(t, int64(8_000_000_000), mapped.CPUNano, "unexpected cpu")
	assert.Equal(t, int64(34_359_738_368), mapped.MemoryBytes, "unexpected memory")
	assert.Equal(t, map[string]string{"role": "manager", "zone": "eu-1"}, mapped.Labels, "unexpected labels")
}

func TestNodeManagerMapNodeUsesSpecRoleIndependentlyOfManagerStatus(t *testing.T) {
	for _, tc := range []struct {
		name          string
		role          dockerswarm.NodeRole
		managerStatus *dockerswarm.ManagerStatus
		wantRole      string
		wantStatus    NodeManagerStatus
	}{
		{
			name: "manager role with manager status", role: dockerswarm.NodeRoleManager,
			managerStatus: &dockerswarm.ManagerStatus{Leader: true},
			wantRole:      "manager", wantStatus: NodeManagerStatusLeader,
		},
		{
			name: "worker role without manager status", role: dockerswarm.NodeRoleWorker,
			wantRole: "worker", wantStatus: NodeManagerStatusWorker,
		},
		{
			name: "worker role while manager status remains", role: dockerswarm.NodeRoleWorker,
			managerStatus: &dockerswarm.ManagerStatus{Reachability: dockerswarm.ReachabilityReachable},
			wantRole:      "worker", wantStatus: NodeManagerStatusReachable,
		},
		{
			name: "manager role before manager status appears", role: dockerswarm.NodeRoleManager,
			wantRole: "manager", wantStatus: NodeManagerStatusWorker,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := dockerswarm.Node{
				ID: "node-transition", Spec: dockerswarm.NodeSpec{Role: tc.role},
				ManagerStatus: tc.managerStatus,
			}

			mapped := (&nodeManager{}).mapNode(node)

			assert.Equal(t, tc.wantRole, mapped.Role)
			assert.Equal(t, tc.wantStatus, mapped.ManagerStatus)
			assert.Nil(t, mapped.Labels)
		})
	}
}
