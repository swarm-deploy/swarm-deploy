package tools

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// DockerNetworkList returns current Docker networks snapshot.
type DockerNetworkList struct {
	reader NetworkReader
}

// NewDockerNetworkList creates docker_network_list component.
func NewDockerNetworkList(networkReader NetworkReader) *DockerNetworkList {
	return &DockerNetworkList{
		reader: networkReader,
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
		Request: struct{}{},
	}
}

// Execute runs docker_network_list tool.
func (l *DockerNetworkList) Execute(ctx context.Context, _ routing.Request) (routing.Response, error) {
	networks, err := l.reader.List(ctx)
	if err != nil {
		return routing.Response{}, fmt.Errorf("list networks: %w", err)
	}

	payload := struct {
		Networks []swarm.Network `json:"networks"`
	}{
		Networks: networks,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
