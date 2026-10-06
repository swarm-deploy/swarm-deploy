package events

import "fmt"

// NetworkCreated is emitted when a managed network is created during sync.
type NetworkCreated struct {
	// NetworkName is the created network name.
	NetworkName string
	// NetworkID is the created swarm network identifier.
	NetworkID string
	// Driver is the created network driver.
	Driver string
}

// Type returns the network-created event type.
func (n *NetworkCreated) Type() Type {
	return TypeNetworkCreated
}

// Message returns a human-readable network-created message.
func (n *NetworkCreated) Message() string {
	return fmt.Sprintf("Network %s created", n.NetworkName)
}

// Details returns network creation details.
func (n *NetworkCreated) Details() map[string]string {
	return map[string]string{
		"network_name": n.NetworkName,
		"network_id":   n.NetworkID,
		"driver":       n.Driver,
	}
}
