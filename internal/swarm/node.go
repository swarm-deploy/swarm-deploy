package swarm

// NodeManagerStatus is a manager role/reachability projection.
type NodeManagerStatus string

const (
	// NodeManagerStatusWorker is used for worker nodes.
	NodeManagerStatusWorker NodeManagerStatus = "worker"
	// NodeManagerStatusLeader is used for manager leader node.
	NodeManagerStatusLeader NodeManagerStatus = "leader"
	// NodeManagerStatusManager is used for manager without explicit reachability.
	NodeManagerStatusManager NodeManagerStatus = "manager"
)

// Node is a persisted/read model of Docker Swarm node.
type Node struct {
	// ID is a unique Docker Swarm node identifier.
	ID string `json:"id"`
	// Hostname is a node hostname reported by Docker.
	Hostname string `json:"hostname"`
	// Status is a current node runtime state.
	Status string `json:"status"`
	// Availability is a desired scheduling availability.
	Availability string `json:"availability"`
	// ManagerStatus is a manager role/reachability projection.
	ManagerStatus NodeManagerStatus `json:"manager_status"`
	// EngineVersion is a Docker engine version on node.
	EngineVersion string `json:"engine_version"`
	// Addr is a node address from node status.
	Addr string `json:"addr"`
	// CPUNano is total CPU capacity in nano-CPU units.
	CPUNano int64 `json:"cpu_nano"`
	// MemoryBytes is total memory capacity in bytes.
	MemoryBytes int64 `json:"memory_bytes"`
	// Labels contains custom Docker node labels.
	Labels map[string]string `json:"labels"`
}
