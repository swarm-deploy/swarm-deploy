package swarm

import (
	"context"
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

type configManager struct {
	dockerClient *client.Client
}

func newConfigManager(dockerClient *client.Client) ConfigManager {
	return &configManager{
		dockerClient: dockerClient,
	}
}

func (m *configManager) Get(ctx context.Context, configName string) (Config, error) {
	config, _, err := m.dockerClient.ConfigInspectWithRaw(ctx, configName)
	if err != nil {
		return Config{}, fmt.Errorf("inspect config %s: %w", configName, err)
	}

	return m.mapConfig(config), nil
}

func (m *configManager) ResolveReference(
	ctx context.Context,
	source,
	target string,
) (*dockerswarm.ConfigReference, error) {
	cfg, err := m.Get(ctx, source)
	if err != nil {
		return nil, err
	}

	ref := &dockerswarm.ConfigReference{
		ConfigID:   cfg.ID,
		ConfigName: cfg.Name,
	}
	if target == "" {
		return ref, nil
	}

	ref.File = &dockerswarm.ConfigReferenceFileTarget{
		Name: target,
		UID:  "0",
		GID:  "0",
		Mode: configFileMode,
	}

	return ref, nil
}

func (*configManager) mapConfig(config dockerswarm.Config) Config {
	return Config{
		ID:        config.ID,
		Name:      config.Spec.Name,
		CreatedAt: config.CreatedAt,
		UpdatedAt: config.UpdatedAt,
		Labels:    config.Spec.Labels,
		Data:      config.Spec.Data,
	}
}
