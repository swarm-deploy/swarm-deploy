package swarm

import (
	"testing"

	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
)

func TestNetworkManagerMapNetworkMapsFields(t *testing.T) {
	network := dockernetwork.Summary{
		Name:       "backend",
		Scope:      "swarm",
		Driver:     "overlay",
		Internal:   true,
		Attachable: true,
		Ingress:    false,
		Options: map[string]string{
			"encrypted": "true",
		},
		Labels: map[string]string{
			"com.example.team": "platform",
		},
	}

	mapped := (&NetworkManager{}).mapNetwork(network)

	assert.Equal(t, "backend", mapped.Name, "unexpected network name")
	assert.Equal(t, "swarm", mapped.Scope, "unexpected scope")
	assert.Equal(t, "overlay", mapped.Driver, "unexpected driver")
	assert.True(t, mapped.Internal, "unexpected internal flag")
	assert.True(t, mapped.Attachable, "unexpected attachable flag")
	assert.False(t, mapped.Ingress, "unexpected ingress flag")
	assert.Equal(t, map[string]string{"encrypted": "true"}, mapped.Options, "unexpected network options")
	assert.Equal(t, map[string]string{"com.example.team": "platform"}, mapped.Labels, "unexpected labels")
}
