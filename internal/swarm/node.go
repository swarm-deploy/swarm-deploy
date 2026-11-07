package swarm

import (
	"errors"
	"strings"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	swarmkitapi "github.com/moby/swarmkit/v2/api"
)

// ErrNodeNotFound means that node does not exist in docker.
var ErrNodeNotFound = errors.New("node not found")

// ErrNodeUpdateConflict means node was changed between inspect and update.
var ErrNodeUpdateConflict = errors.New("node update conflict")

// NodeManagerStatus is a manager role/reachability projection.
type NodeManagerStatus string

const (
	// NodeManagerStatusWorker is used for worker nodes.
	NodeManagerStatusWorker NodeManagerStatus = "worker"
	// NodeManagerStatusLeader is used for manager leader node.
	NodeManagerStatusLeader NodeManagerStatus = "leader"
	// NodeManagerStatusManager is used for manager without explicit reachability.
	NodeManagerStatusManager NodeManagerStatus = "manager"
	// NodeManagerStatusReachable is used for reachable manager node.
	NodeManagerStatusReachable NodeManagerStatus = "reachable"
	// NodeManagerStatusUnreachable is used for unreachable manager node.
	NodeManagerStatusUnreachable NodeManagerStatus = "unreachable"
)

// Node is a persisted/read model of Docker Swarm node.
type Node struct {
	// ID is a unique Docker Swarm node identifier.
	ID string `json:"id"`
	// Role is the node's observed Swarm role used by placement constraints.
	Role string `json:"-"`
	// Hostname is a node hostname reported by Docker.
	Hostname string `json:"hostname"`
	// PlatformOS is the node operating system used by placement constraints.
	PlatformOS string `json:"-"`
	// PlatformArchitecture is the node architecture used by placement constraints.
	PlatformArchitecture string `json:"-"`
	// Status is a current node runtime state.
	Status string `json:"status"`
	// Availability is a desired scheduling availability.
	Availability string `json:"availability"`
	// ManagerStatus is a manager role/reachability projection.
	ManagerStatus NodeManagerStatus `json:"manager_status"`
	// EngineVersion is a Docker engine version on node.
	EngineVersion string `json:"engine_version"`
	// EngineLabels contains Docker engine labels used by placement constraints.
	EngineLabels map[string]string `json:"-"`
	// Addr is a node address from node status.
	Addr string `json:"addr"`
	// CPUNano is total CPU capacity in nano-CPU units.
	CPUNano int64 `json:"cpu_nano"`
	// MemoryBytes is total memory capacity in bytes.
	MemoryBytes int64 `json:"memory_bytes"`
	// Labels contains custom Docker node labels.
	Labels map[string]string `json:"labels"`
}

// ConstraintNode returns the current node attributes used by SwarmKit placement constraints.
func (n Node) ConstraintNode() *swarmkitapi.Node {
	role := swarmkitapi.NodeRoleWorker
	if strings.EqualFold(n.Role, string(dockerswarm.NodeRoleManager)) {
		role = swarmkitapi.NodeRoleManager
	}
	return &swarmkitapi.Node{
		ID: n.ID, Role: role,
		Spec: swarmkitapi.NodeSpec{Annotations: swarmkitapi.Annotations{Labels: n.Labels}},
		Description: &swarmkitapi.NodeDescription{
			Hostname: n.Hostname,
			Platform: &swarmkitapi.Platform{OS: n.PlatformOS, Architecture: n.PlatformArchitecture},
			Engine:   &swarmkitapi.EngineDescription{Labels: n.EngineLabels},
		},
		Status: swarmkitapi.NodeStatus{Addr: n.Addr},
	}
}

// NodeLabelUpdateRequest describes a Docker node label mutation.
type NodeLabelUpdateRequest struct {
	// NodeID is a Docker Swarm node identifier.
	NodeID string
	// Key is a Docker node label key.
	Key string
	// Value is a Docker node label value.
	Value string
}
