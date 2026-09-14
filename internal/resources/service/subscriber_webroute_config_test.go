package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestSubscriberLoadWebRouteConfigPrefersRepositoryContent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "pomerium.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("from-repository"), 0o600))

	reader := &fakeSubscriberConfigReader{
		configs: map[string]swarm.Config{
			"prod_pomerium_config": {Data: []byte("from-docker")},
		},
	}
	sub := &Subscriber{
		configs:    reader,
		fileSystem: fs.NewLocalFileSystem(),
	}
	desired := &compose.File{
		Path: filepath.Join(dir, "compose.yaml"),
		Compose: compose.Compose{
			Configs: compose.SharedObjects{
				"pomerium_config": {File: "./pomerium.yaml"},
			},
		},
	}
	desiredRef := &compose.ObjectRef{Source: "pomerium_config"}

	config, ok := sub.loadWebRouteConfig(
		context.Background(),
		"prod",
		"pomerium",
		swarm.ServiceConfig{
			ConfigName: "prod_pomerium_config",
			Target:     "/etc/pomerium/config.yaml",
		},
		desiredRef,
		desired,
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
		nil,
	)
	require.True(t, ok)
	require.NotNil(t, config)

	var out bytes.Buffer
	require.NoError(t, config.Read(context.Background(), &out))
	assert.Equal(t, "from-docker", out.String())
	assert.Equal(t, []string{"prod_pomerium_config"}, reader.calls)
}

func TestMatchDesiredConfigRefUsesTargetBeforeOrder(t *testing.T) {
	refs := []compose.ObjectRef{
		{Source: "first", Target: "/etc/first.yaml"},
		{Source: "pomerium", Target: "/etc/pomerium/config.yaml"},
	}

	ref := matchDesiredConfigRef(refs, swarm.ServiceConfig{Target: "/etc/pomerium/config.yaml"}, 0)

	require.NotNil(t, ref)
	assert.Equal(t, "pomerium", ref.Source)
}
