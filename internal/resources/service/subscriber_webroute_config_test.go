package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestSubscriberLoadWebRouteConfigPrefersRepositoryContent(t *testing.T) {
	reader := &fakeSubscriberConfigReader{
		configs: map[string]swarm.Config{
			"prod_pomerium_config": {Data: []byte("from-docker")},
		},
	}
	sub := &Subscriber{configs: reader}

	config, ok := sub.loadWebRouteConfig(
		context.Background(),
		"prod",
		"pomerium",
		swarm.ServiceConfig{
			ConfigName: "prod_pomerium_config",
			Target:     "/etc/pomerium/config.yaml",
		},
		map[string][]byte{
			"prod_pomerium_config": []byte("from-repository"),
		},
	)
	require.True(t, ok)
	require.NotNil(t, config)

	var out bytes.Buffer
	require.NoError(t, config.Read(context.Background(), &out))
	assert.Equal(t, "from-repository", out.String())
	assert.Empty(t, reader.calls)
}

func TestSubscriberLoadWebRouteConfigFallsBackToDocker(t *testing.T) {
	reader := &fakeSubscriberConfigReader{
		configs: map[string]swarm.Config{
			"prod_pomerium_config": {Data: []byte("from-docker")},
		},
	}
	sub := &Subscriber{configs: reader}

	config, ok := sub.loadWebRouteConfig(
		context.Background(),
		"prod",
		"pomerium",
		swarm.ServiceConfig{
			ConfigName: "prod_pomerium_config",
			Target:     "/etc/pomerium/config.yaml",
		},
		nil,
	)
	require.True(t, ok)
	require.NotNil(t, config)

	var out bytes.Buffer
	require.NoError(t, config.Read(context.Background(), &out))
	assert.Equal(t, "from-docker", out.String())
	assert.Equal(t, []string{"prod_pomerium_config"}, reader.calls)
}
