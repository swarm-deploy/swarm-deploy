package swarm

import (
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginManagerMapPluginMapsFields(t *testing.T) {
	plugin := dockertypes.Plugin{
		ID:              "plugin-id",
		Name:            "local/my-plugin",
		PluginReference: "docker.io/local/my-plugin:latest",
		Enabled:         true,
		Config: dockertypes.PluginConfig{
			Description: "Demo plugin",
			Interface: dockertypes.PluginConfigInterface{
				Types: []dockertypes.PluginInterfaceType{
					{
						Prefix:     "docker",
						Capability: "logdriver",
						Version:    "1.0",
					},
				},
			},
		},
	}

	mapped := (&PluginManager{}).mapPlugin(plugin)

	assert.Equal(t, "plugin-id", mapped.ID, "unexpected plugin id")
	assert.Equal(t, "local/my-plugin", mapped.Name, "unexpected plugin name")
	assert.Equal(t, "Demo plugin", mapped.Description, "unexpected description")
	assert.True(t, mapped.Enabled, "unexpected enabled flag")
	assert.Equal(t, "docker.io/local/my-plugin:latest", mapped.PluginReference, "unexpected plugin reference")
	require.Len(t, mapped.Capabilities, 1, "unexpected capabilities count")
	assert.Equal(t, "docker.logdriver/1.0", mapped.Capabilities[0], "unexpected capability")
}
