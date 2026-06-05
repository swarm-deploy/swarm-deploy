package graph

// Graph is a service dependency graph.
type Graph struct {
	// Nodes contains all graph nodes.
	Nodes []Node
}

// Kind is a graph node kind.
type Kind string

const (
	// KindService marks service node kind.
	KindService = "service"
)

// Node is a single graph node.
type Node struct {
	// Name is a unique node name.
	Name string `json:"name"`
	// Kind is a node entity kind.
	Kind Kind `json:"kind"`
	// Endpoints contains public endpoints resolved for the node.
	Endpoints []string `json:"endpoints"`
	// Depends contains direct dependency nodes.
	Depends []Node `json:"depends"`
}
