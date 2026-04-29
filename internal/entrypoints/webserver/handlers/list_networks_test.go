package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type fakeNetworksReader struct {
	list []swarm.Network
	err  error
}

func (f fakeNetworksReader) List(_ context.Context) ([]swarm.Network, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.list, nil
}

func TestHandlerListNetworks(t *testing.T) {
	t.Parallel()

	h := &handler{
		networks: fakeNetworksReader{
			list: []swarm.Network{
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
			},
		},
	}

	resp, err := h.ListNetworks(context.Background())
	require.NoError(t, err, "list networks")
	require.NotNil(t, resp, "response must be set")
	require.Len(t, resp.Networks, 1, "expected one network")
	assert.Equal(t, "shared-backend", resp.Networks[0].Name, "unexpected network name")
	require.True(t, resp.Networks[0].Labels.IsSet(), "labels must be set")
	assert.Equal(
		t,
		"true",
		resp.Networks[0].Labels.Value["org.swarm-deploy.network.managed"],
		"unexpected managed label",
	)
}

func TestHandlerListNetworksReturnsReaderError(t *testing.T) {
	t.Parallel()

	h := &handler{
		networks: fakeNetworksReader{
			err: errors.New("unreachable"),
		},
	}

	_, err := h.ListNetworks(context.Background())
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "list docker networks", "unexpected error")
}
