package swarm

import (
	"context"
	"fmt"
	"sort"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// PluginManager reads current Docker plugins snapshot.
type PluginManager struct {
	dockerClient *client.Client
}

func newPluginManager(dockerClient *client.Client) *PluginManager {
	return &PluginManager{
		dockerClient: dockerClient,
	}
}

// List returns current Docker plugins snapshot.
func (m *PluginManager) List(ctx context.Context) ([]Plugin, error) {
	plugins, err := m.dockerClient.PluginList(ctx, filters.NewArgs())
	if err != nil {
		return nil, fmt.Errorf("list docker plugins: %w", err)
	}

	mapped := make([]Plugin, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}

		mapped = append(mapped, m.mapPlugin(*plugin))
	}
	m.sortPlugins(mapped)

	return mapped, nil
}

func (*PluginManager) mapPlugin(plugin dockertypes.Plugin) Plugin {
	capabilities := make([]string, 0, len(plugin.Config.Interface.Types))
	for _, ifaceType := range plugin.Config.Interface.Types {
		capabilities = append(capabilities, ifaceType.String())
	}

	return Plugin{
		ID:              plugin.ID,
		Name:            plugin.Name,
		Description:     plugin.Config.Description,
		Enabled:         plugin.Enabled,
		PluginReference: plugin.PluginReference,
		Capabilities:    capabilities,
	}
}

func (*PluginManager) sortPlugins(plugins []Plugin) {
	sort.Slice(plugins, func(i, j int) bool {
		if plugins[i].Name != plugins[j].Name {
			return plugins[i].Name < plugins[j].Name
		}

		return plugins[i].ID < plugins[j].ID
	})
}
