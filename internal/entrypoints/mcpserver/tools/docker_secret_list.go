package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
)

// DockerSecretList returns current Docker secrets snapshot.
type DockerSecretList struct {
	inspector SecretInspector
}

// NewDockerSecretList creates docker_secret_list component.
func NewDockerSecretList(secretInspector SecretInspector) *DockerSecretList {
	return &DockerSecretList{
		inspector: secretInspector,
	}
}

// Definition returns tool metadata visible to the model.
func (l *DockerSecretList) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "docker_secret_list",
		Description: "Returns current Docker secrets snapshot.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// Execute runs docker_secret_list tool.
func (l *DockerSecretList) Execute(ctx context.Context, _ routing.Request) (routing.Response, error) {
	secrets, err := l.inspector.InspectSecrets(ctx)
	if err != nil {
		return routing.Response{}, fmt.Errorf("inspect secrets: %w", err)
	}

	payload := struct {
		Secrets []inspector.SecretInfo `json:"secrets"`
	}{
		Secrets: secrets,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
