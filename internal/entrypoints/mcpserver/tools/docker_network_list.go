package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
)

// DockerNetworkList returns current Docker networks snapshot.
type DockerNetworkList struct {
	inspector NetworkInspector
}

// NewDockerNetworkList creates docker_network_list component.
func NewDockerNetworkList(networkInspector NetworkInspector) *DockerNetworkList {
	return &DockerNetworkList{
		inspector: networkInspector,
	}
}

// Definition returns tool metadata visible to the model.
func (l *DockerNetworkList) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "docker_network_list",
		Description: "Returns current Docker networks snapshot.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// Execute runs docker_network_list tool.
func (l *DockerNetworkList) Execute(ctx context.Context, _ routing.Request) (routing.Response, error) {
	networks, err := l.inspector.InspectNetworks(ctx)
	if err != nil {
		return routing.Response{}, fmt.Errorf("inspect networks: %w", err)
	}

	payload := struct {
		Networks []inspector.NetworkInfo `json:"networks"`
	}{
		Networks: networks,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
