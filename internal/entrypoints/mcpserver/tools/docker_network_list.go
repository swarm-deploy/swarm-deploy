// docker_network_list.go

// This file mirrors the design of other tools while implementing "docker_network_list" functionality.
// Metadata under the method Definition.

package tools

import (
	"context"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"log"
)

type DockerNetworkList struct {}

// Definition contains metadata for the tool.
var Definition = struct {
	Name        string
	Description string
}{
	Name:        "docker_network_list",
	Description: "Lists Docker networks as part of MCP tools",
}

// Execute lists all Docker networks.
func (d *DockerNetworkList) Execute(ctx context.Context) ([]types.NetworkResource, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion("1.40"))
	if err != nil {
		log.Fatalf("Error creating Docker client: %v", err)
	}

	networks, err := cli.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		return nil, err
	}

	return networks, nil
}