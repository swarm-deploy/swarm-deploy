package graph

// Graph is a service dependency graph.
type Graph struct {
	// Nodes contains all graph nodes.
	Nodes []Node
}

// Kind is a graph node kind.
type Kind string

const (
	// KindApplication marks application service nodes.
	KindApplication Kind = "application"
	// KindMonitoring marks monitoring service nodes.
	KindMonitoring Kind = "monitoring"
	// KindDelivery marks delivery service nodes.
	KindDelivery Kind = "delivery"
	// KindReverseProxy marks reverse proxy service nodes.
	KindReverseProxy Kind = "reverseProxy"
	// KindDeploymentManagementSystem marks deployment management service nodes.
	KindDeploymentManagementSystem Kind = "deploymentManagementSystem"
	// KindDatabase marks database service nodes.
	KindDatabase Kind = "database"
	// KindSecretManager marks secret manager service nodes.
	KindSecretManager Kind = "secretManager"
)

// Node is a single graph node.
type Node struct {
	// Name is a unique node name.
	Name string `json:"name"`
	// Kind is a node entity kind.
	Kind Kind `json:"kind"`
	// Endpoints contains public endpoints resolved for the node.
	Endpoints []string `json:"endpoints"`
	// Depends contains direct dependency node names.
	Depends []string `json:"depends"`
}
