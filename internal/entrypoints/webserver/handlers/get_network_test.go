package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestHandlerGetNetwork(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	networksReader := swarm.NewMockNetworkManager(ctrl)
	h := &handler{networks: networksReader}
	createdAt := time.Date(2026, time.November, 27, 18, 0, 0, 0, time.UTC)

	networksReader.EXPECT().
		Get(gomock.Any(), "network-id", dockernetwork.InspectOptions{Verbose: true, Scope: "swarm"}).
		Return(swarm.Network{
			ID:         "network-id",
			Name:       "app_backend",
			CreatedAt:  createdAt,
			Scope:      "swarm",
			Driver:     "overlay",
			EnableIPv4: true,
			Attachable: true,
			Labels: map[string]string{
				"com.docker.stack.namespace":       "app",
				"org.swarm-deploy.network.managed": "true",
				"example.com/network-description":  "backend",
			},
			Options:    map[string]string{"encrypted": ""},
			IPAMDriver: "default",
			IPAM: []dockernetwork.IPAMConfig{{
				Subnet: "10.0.1.0/24", Gateway: "10.0.1.1", IPRange: "10.0.1.0/25",
				AuxAddress: map[string]string{"router": "10.0.1.2"},
			}},
			Services: map[string]dockernetwork.ServiceInfo{
				"app_api": {VIP: "10.0.1.10"},
			},
			Peers: []dockernetwork.PeerInfo{{Name: "node-b", IP: "192.0.2.2"}, {Name: "node-a", IP: "192.0.2.1"}},
		}, nil)

	response, err := h.GetNetwork(context.Background(), generated.GetNetworkParams{NetworkID: "network-id"})
	require.NoError(t, err, "get network")
	item, ok := response.(*generated.NetworkInfo)
	require.True(t, ok, "response must contain network details")
	assert.Equal(t, "app_backend", item.Name, "unexpected network name")
	assert.Equal(t, "app", item.StackName.Value, "unexpected stack")
	assert.True(t, item.Managed, "network must be managed")
	assert.Equal(t, createdAt, item.CreatedAt.Value, "unexpected creation time")
	assert.Equal(t, "10.0.1.0/25", item.Ipam[0].IPRange.Value, "unexpected IP range")
	assert.Equal(t, "10.0.1.2", item.Ipam[0].AuxAddresses.Value["router"], "unexpected auxiliary address")
	assert.Equal(t, []generated.NetworkServiceInfo{{Name: "app_api", Vip: generated.NewOptString("10.0.1.10")}}, item.Services, "unexpected services")
	assert.Equal(t, []generated.NetworkPeerInfo{{Name: "node-a", IP: "192.0.2.1"}, {Name: "node-b", IP: "192.0.2.2"}}, item.Peers, "unexpected peers")
	assert.Equal(t, generated.NetworkInfoLabels{"example.com/network-description": "backend"}, item.Labels.Value, "unexpected public labels")
}

func TestHandlerGetNetworkReturnsNotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	networksReader := swarm.NewMockNetworkManager(ctrl)
	h := &handler{networks: networksReader}

	networksReader.EXPECT().
		Get(gomock.Any(), "missing", dockernetwork.InspectOptions{Verbose: true, Scope: "swarm"}).
		Return(swarm.Network{}, swarm.ErrNetworkNotFound)

	_, err := h.GetNetwork(context.Background(), generated.GetNetworkParams{NetworkID: "missing"})
	require.Error(t, err, "missing network must fail")
	assert.True(t, errors.Is(err, swarm.ErrNetworkNotFound), "not-found cause must be preserved")
}
