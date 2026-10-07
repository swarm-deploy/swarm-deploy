package swarm

import (
	"context"
	"fmt"
	"sort"

	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
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
		return Config{}, fmt.Errorf("inspect config %s: %w", configName, dockerAPIError(err))
	}

	return m.mapConfig(config), nil
}

func (m *configManager) ListStack(ctx context.Context, stackName string) ([]Config, error) {
	configs, err := m.dockerClient.ConfigList(ctx, dockerswarm.ConfigListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", stackNamespaceLabelKey+"="+stackName),
			filters.Arg(
				"label",
				labelsdict.RotatedResourceManagedLabelKey+"="+labelsdict.RotatedResourceManagedLabelValue,
			),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list docker configs for stack %s: %w", stackName, dockerAPIError(err))
	}

	mapped := make([]Config, len(configs))
	for i, cfg := range configs {
		mapped[i] = m.mapConfig(cfg)
	}
	sort.Slice(mapped, func(i, j int) bool {
		if mapped[i].Name != mapped[j].Name {
			return mapped[i].Name < mapped[j].Name
		}

		return mapped[i].ID < mapped[j].ID
	})

	return mapped, nil
}

func (m *configManager) Remove(ctx context.Context, configID string) error {
	if err := m.dockerClient.ConfigRemove(ctx, configID); err != nil {
		return fmt.Errorf("remove docker config %s: %w", configID, dockerAPIError(err))
	}

	return nil
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
