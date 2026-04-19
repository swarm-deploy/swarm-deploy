package inspector

import (
	"context"
	"fmt"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
)

// InspectPlugins returns current Docker plugins snapshot.
func (i *Inspector) InspectPlugins(ctx context.Context) ([]PluginInfo, error) {
	plugins, err := i.dockerClient.PluginList(ctx, filters.NewArgs())
	if err != nil {
		return nil, fmt.Errorf("list docker plugins: %w", err)
	}

	mapped := make([]PluginInfo, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}

		mapped = append(mapped, toPluginInfo(*plugin))
	}
	sortPluginInfos(mapped)

	return mapped, nil
}

func toPluginInfo(plugin dockertypes.Plugin) PluginInfo {
	capabilities := make([]string, 0, len(plugin.Config.Interface.Types))
	for _, ifaceType := range plugin.Config.Interface.Types {
		capabilities = append(capabilities, ifaceType.String())
	}

	return PluginInfo{
		ID:              plugin.ID,
		Name:            plugin.Name,
		Description:     plugin.Config.Description,
		Enabled:         plugin.Enabled,
		PluginReference: plugin.PluginReference,
		Capabilities:    capabilities,
	}
}
