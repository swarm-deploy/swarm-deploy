package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestHandlerListNetworks(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	networksReader := swarm.NewMockNetworkManager(ctrl)
	h := &handler{
		networks: networksReader,
	}

	networksReader.EXPECT().
		List(gomock.Any(), swarm.ListNetworksFilter{Stacks: []string{"app", "infra"}}).
		Return([]swarm.Network{
			{
				ID:         "net-1",
				Name:       "shared-backend",
				Scope:      "swarm",
				Driver:     "overlay",
				Attachable: true,
				Labels: map[string]string{
					"org.swarm-deploy.network.managed": "true",
				},
				Options: map[string]string{
					"encrypted": "true",
				},
			},
		}, nil)

	resp, err := h.ListNetworks(context.Background(), generated.ListNetworksParams{
		Stack: []string{"app", "infra"},
	})
	require.NoError(t, err, "list networks")
	require.NotNil(t, resp, "response must be set")
	require.Len(t, resp.Networks, 1, "expected one network")
	assert.Equal(t, "shared-backend", resp.Networks[0].Name, "unexpected network name")
	require.True(t, resp.Networks[0].Labels.IsSet(), "labels must be set")
	assert.True(t, resp.Networks[0].Managed, "network must be managed")
}

func TestHandlerListNetworksReturnsReaderError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	networksReader := swarm.NewMockNetworkManager(ctrl)
	h := &handler{
		networks: networksReader,
	}

	networksReader.EXPECT().
		List(gomock.Any(), swarm.ListNetworksFilter{}).
		Return(nil, errors.New("unreachable"))

	_, err := h.ListNetworks(context.Background(), generated.ListNetworksParams{})
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "list docker networks", "unexpected error")
}
