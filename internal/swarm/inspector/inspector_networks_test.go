package inspector

import (
	"context"
	"testing"

	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectorInspectNetworksFailsWithoutDockerClient(t *testing.T) {
	inspector := &Inspector{}

	_, err := inspector.InspectNetworks(context.Background())
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "docker api client is not initialized", "unexpected error")
}

func TestToNetworkInfoMapsFields(t *testing.T) {
	network := dockernetwork.Summary{
		Name:       " backend ",
		Scope:      " swarm ",
		Driver:     " overlay ",
		Internal:   true,
		Attachable: true,
		Ingress:    false,
		Labels: map[string]string{
			"com.example.env": "prod",
		},
	}

	mapped := toNetworkInfo(network)

	assert.Equal(t, "backend", mapped.Name, "unexpected network name")
	assert.Equal(t, "swarm", mapped.Scope, "unexpected scope")
	assert.Equal(t, "overlay", mapped.Driver, "unexpected driver")
	assert.True(t, mapped.Internal, "unexpected internal flag")
	assert.True(t, mapped.Attachable, "unexpected attachable flag")
	assert.False(t, mapped.Ingress, "unexpected ingress flag")
	assert.Equal(t, "prod", mapped.Labels["com.example.env"], "unexpected labels")
}
