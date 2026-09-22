package network

import "github.com/swarm-deploy/swarm-deploy/internal/swarm"

type Network struct {
	swarm.Network
}

type ServiceRef struct {
	Stack string `json:"stack"`
	Name  string `json:"name"`
}
