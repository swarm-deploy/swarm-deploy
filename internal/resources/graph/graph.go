package graph

type Graph struct {
	Nodes []Node
}

type Kind string

const (
	KindService = "service"
)

type Node struct {
	Name    string `json:"name"`
	Kind    Kind   `json:"kind"`
	Depends []Node `json:"depends"`
}
