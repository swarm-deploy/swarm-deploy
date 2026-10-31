package converter

import (
	"fmt"
	"maps"
	"path"
	"slices"

	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-units"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func serviceMounts(stackName string, service compose.Service, definitions compose.Volumes) ([]mount.Mount, error) {
	result := make([]mount.Mount, 0, len(service.Volumes.Volumes))
	targets := map[string]bool{}
	for _, volume := range service.Volumes.Volumes {
		if volume == nil {
			return nil, fmt.Errorf("volumes: null mount")
		}
		if !path.IsAbs(volume.Target) || targets[volume.Target] {
			return nil, fmt.Errorf("volumes.target: expected unique absolute path")
		}
		targets[volume.Target] = true
		converted, err := convertMount(stackName, *volume, definitions)
		if err != nil {
			return nil, fmt.Errorf("volumes[%s]: %w", volume.Target, err)
		}
		result = append(result, converted)
	}
	return result, nil
}

func convertMount(stackName string, volume compose.ServiceVolume, definitions compose.Volumes) (mount.Mount, error) {
	if !slices.Contains([]mount.Consistency{"", "default", "consistent", "cached", "delegated"}, volume.Consistency) {
		return mount.Mount{}, fmt.Errorf("consistency: unsupported value")
	}
	kind := mount.Type(volume.Type)
	if kind == "" {
		kind = mount.TypeVolume
	}
	result := mount.Mount{Type: kind, Source: volume.Source, Target: volume.Target,
		ReadOnly: volume.ReadOnly, Consistency: volume.Consistency}
	switch kind {
	case mount.TypeVolume:
		return namedMount(stackName, volume, definitions, result)
	case mount.TypeBind:
		return bindMount(volume, result)
	case mount.TypeTmpfs:
		return tmpfsMount(volume, result)
	case mount.TypeNamedPipe, mount.TypeCluster, mount.TypeImage:
		return result, fmt.Errorf("type: unsupported mount type")
	default:
		return result, fmt.Errorf("type: unsupported mount type")
	}
}

func bindMount(volume compose.ServiceVolume, result mount.Mount) (mount.Mount, error) {
	if volume.Volume != nil || volume.Tmpfs != nil {
		return result, fmt.Errorf("bind: incompatible mount options")
	}
	if !path.IsAbs(volume.Source) {
		return result, fmt.Errorf("bind.source: must be prepared as an absolute path")
	}
	if volume.Bind == nil {
		return result, nil
	}
	if volume.Bind.CreateHostPath != nil && *volume.Bind.CreateHostPath {
		return result, fmt.Errorf("bind.create_host_path: unsupported by swarm")
	}
	propagation := volume.Bind.Propagation
	if propagation != "" && !slices.Contains(mount.Propagations, propagation) {
		return result, fmt.Errorf("bind.propagation: invalid value")
	}
	result.BindOptions = &mount.BindOptions{Propagation: propagation}
	return result, nil
}

const maxTmpfsMode = 0o7777

func tmpfsMount(volume compose.ServiceVolume, result mount.Mount) (mount.Mount, error) {
	if volume.Source != "" || volume.Bind != nil || volume.Volume != nil {
		return result, fmt.Errorf("tmpfs: incompatible source or mount options")
	}
	if volume.Tmpfs == nil {
		return result, nil
	}
	if volume.Tmpfs.Mode > maxTmpfsMode {
		return result, fmt.Errorf("tmpfs.mode: invalid file mode")
	}
	size, err := byteSize(volume.Tmpfs.Size)
	if err != nil {
		return result, fmt.Errorf("tmpfs.size: %w", err)
	}
	result.TmpfsOptions = &mount.TmpfsOptions{SizeBytes: size, Mode: volume.Tmpfs.Mode}
	return result, nil
}

func namedMount(
	stackName string, volume compose.ServiceVolume, definitions compose.Volumes, result mount.Mount,
) (mount.Mount, error) {
	if volume.Bind != nil || volume.Tmpfs != nil {
		return result, fmt.Errorf("volume: incompatible mount options")
	}
	result.VolumeOptions = &mount.VolumeOptions{}
	if volume.Volume != nil {
		result.VolumeOptions.NoCopy = volume.Volume.Nocopy
		result.VolumeOptions.Subpath = volume.Volume.Subpath
	}
	if volume.Source == "" {
		return result, nil
	}
	definition, ok := definitions[volume.Source]
	if !ok {
		return result, fmt.Errorf("source: undefined named volume")
	}
	if definition == nil {
		definition = &compose.Volume{}
	}
	result.Source = resourceName(stackName, volume.Source, definition.Name, definition.External)
	if definition.External {
		return result, nil
	}
	result.VolumeOptions.Labels = stackLabels(stackName, definition.Labels)
	if definition.Driver != "" || len(definition.DriverOpts) > 0 {
		result.VolumeOptions.DriverConfig = &mount.Driver{Name: definition.Driver, Options: maps.Clone(definition.DriverOpts)}
	}
	return result, nil
}

func byteSize(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	size, err := units.RAMInBytes(value)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("invalid nonnegative byte size")
	}
	return size, nil
}
