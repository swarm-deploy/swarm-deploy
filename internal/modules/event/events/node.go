package events

import "fmt"

// NodeConnected is emitted when a swarm node becomes ready.
type NodeConnected struct {
	// NodeID is a Docker Swarm node identifier.
	NodeID string
	// NodeName is a Docker Swarm node hostname.
	NodeName string
	// Status is a current Docker Swarm node status.
	Status string
}

func (n *NodeConnected) Type() Type {
	return TypeNodeConnected
}

func (n *NodeConnected) Message() string {
	return fmt.Sprintf("Node %s connected", n.displayName())
}

func (n *NodeConnected) Details() map[string]string {
	return nodeDetails(n.NodeID, n.NodeName, n.Status)
}

func (n *NodeConnected) displayName() string {
	return nodeDisplayName(n.NodeName, n.NodeID)
}

// NodeDisconnected is emitted when a swarm node leaves ready state.
type NodeDisconnected struct {
	// NodeID is a Docker Swarm node identifier.
	NodeID string
	// NodeName is a Docker Swarm node hostname.
	NodeName string
	// Status is a current Docker Swarm node status.
	Status string
}

func (n *NodeDisconnected) Type() Type {
	return TypeNodeDisconnected
}

func (n *NodeDisconnected) Message() string {
	return fmt.Sprintf("Node %s disconnected", n.displayName())
}

func (n *NodeDisconnected) Details() map[string]string {
	return nodeDetails(n.NodeID, n.NodeName, n.Status)
}

func (n *NodeDisconnected) displayName() string {
	return nodeDisplayName(n.NodeName, n.NodeID)
}

func nodeDisplayName(nodeName, nodeID string) string {
	if nodeName != "" {
		return nodeName
	}
	if nodeID != "" {
		return nodeID
	}

	return "unknown"
}

func nodeDetails(nodeID, nodeName, status string) map[string]string {
	details := map[string]string{}
	if nodeID != "" {
		details["node_id"] = nodeID
	}
	if nodeName != "" {
		details["node_name"] = nodeName
	}
	if status != "" {
		details["status"] = status
	}

	return details
}
