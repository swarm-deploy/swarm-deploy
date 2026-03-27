package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerNetworkListExecute(t *testing.T) {
	tool := NewDockerNetworkList(&fakeNetworkInspector{
		networks: []inspector.NetworkInfo{
			{
				Name:       "backend",
				Scope:      "swarm",
				Driver:     "overlay",
				Internal:   true,
				Attachable: true,
				Ingress:    false,
				Labels: map[string]string{
					"com.example.team": "platform",
				},
			},
		},
	})

	response, err := tool.Execute(context.Background(), routing.Request{})
	require.NoError(t, err, "execute docker_network_list")

	var payload struct {
		Networks []inspector.NetworkInfo `json:"networks"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Networks, 1, "expected one network")
	assert.Equal(t, "backend", payload.Networks[0].Name, "unexpected network name")
	assert.True(t, payload.Networks[0].Internal, "unexpected internal flag")
}

func TestDockerNetworkListExecuteFailsOnInspectError(t *testing.T) {
	tool := NewDockerNetworkList(&fakeNetworkInspector{
		err: errors.New("docker unavailable"),
	})

	_, err := tool.Execute(context.Background(), routing.Request{})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "inspect networks", "unexpected error")
}
