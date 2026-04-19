package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

// DockerSecretList returns current Docker secrets snapshot.
type DockerSecretList struct {
	secrets SecretReader
}

// NewDockerSecretList creates docker_secret_list component.
func NewDockerSecretList(secretReader SecretReader) *DockerSecretList {
	return &DockerSecretList{
		secrets: secretReader,
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
	secrets, err := l.secrets.List(ctx)
	if err != nil {
		return routing.Response{}, fmt.Errorf("list secrets: %w", err)
	}

	payload := struct {
		Secrets []swarm.Secret `json:"secrets"`
	}{
		Secrets: secrets,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
