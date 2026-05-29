package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestDockerPluginListExecute(t *testing.T) {
	tool := NewDockerPluginList(&fakePluginReader{
		plugins: []swarm.Plugin{
			{
				ID:          "plugin-1",
				Name:        "local/my-plugin",
				Description: "Demo plugin",
				Enabled:     true,
				Capabilities: []string{
					"docker.logdriver/1.0",
				},
			},
		},
	})

	response, err := tool.Execute(context.Background(), routing.Request{})
	require.NoError(t, err, "execute docker_plugin_list")

	var payload struct {
		Plugins []swarm.Plugin `json:"plugins"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Plugins, 1, "expected one plugin")
	assert.Equal(t, "plugin-1", payload.Plugins[0].ID, "unexpected plugin id")
	assert.Equal(t, "local/my-plugin", payload.Plugins[0].Name, "unexpected plugin name")
	assert.True(t, payload.Plugins[0].Enabled, "unexpected enabled flag")
}

func TestDockerPluginListExecuteFailsOnInspectError(t *testing.T) {
	tool := NewDockerPluginList(&fakePluginReader{
		err: errors.New("docker unavailable"),
	})

	_, err := tool.Execute(context.Background(), routing.Request{})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "list plugins", "unexpected error")
}
