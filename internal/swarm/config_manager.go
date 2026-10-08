package swarm

import (
	"context"
	"fmt"
	"sort"

	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

const stackResourceFilterCount = 2

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

func (m *configManager) List(ctx context.Context, filter ListConfigsFilter) ([]Config, error) {
	configs, err := m.dockerClient.ConfigList(ctx, dockerswarm.ConfigListOptions{
		Filters: buildConfigListFilters(filter),
	})
	if err != nil {
		return nil, fmt.Errorf("list docker configs: %w", err)
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

func buildConfigListFilters(filter ListConfigsFilter) filters.Args {
	filterArgs := make([]filters.KeyValuePair, 0, len(filter.Names)+len(filter.Labels)+1)
	for _, name := range filter.Names {
		filterArgs = append(filterArgs, filters.Arg("name", name))
	}
	for key, value := range filter.Labels {
		filterArgs = append(filterArgs, filters.Arg("label", key+"="+value))
	}
	if filter.StackName != "" {
		filterArgs = append(filterArgs, filters.Arg("label", stackNamespaceLabelKey+"="+filter.StackName))
	}

	return filters.NewArgs(filterArgs...)
}

func (m *configManager) Create(ctx context.Context, req CreateConfigRequest) (string, error) {
	spec := dockerswarm.ConfigSpec{
		Annotations: dockerswarm.Annotations{Name: req.Name, Labels: req.Labels},
		Data:        req.Data,
	}
	if req.TemplateDriver != "" {
		spec.Templating = &dockerswarm.Driver{Name: req.TemplateDriver}
	}

	created, err := m.dockerClient.ConfigCreate(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("create docker config %s: %w", req.Name, err)
	}

	return created.ID, nil
}

func (m *configManager) Remove(ctx context.Context, configID string) error {
	if err := m.dockerClient.ConfigRemove(ctx, configID); err != nil {
		return fmt.Errorf("remove docker config %s: %w", configID, err)
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
