package tools

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// DockerPluginList returns current Docker plugins snapshot.
type DockerPluginList struct {
	reader PluginReader
}

// NewDockerPluginList creates docker_plugin_list component.
func NewDockerPluginList(pluginReader PluginReader) *DockerPluginList {
	return &DockerPluginList{
		reader: pluginReader,
	}
}

// Definition returns tool metadata visible to the model.
func (l *DockerPluginList) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "docker_plugin_list",
		Description: "Returns current Docker plugins snapshot.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Request: struct{}{},
	}
}

// Execute runs docker_plugin_list tool.
func (l *DockerPluginList) Execute(ctx context.Context, _ routing.Request) (routing.Response, error) {
	plugins, err := l.reader.List(ctx)
	if err != nil {
		return routing.Response{}, fmt.Errorf("list plugins: %w", err)
	}

	payload := struct {
		Plugins []swarm.Plugin `json:"plugins"`
	}{
		Plugins: plugins,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
