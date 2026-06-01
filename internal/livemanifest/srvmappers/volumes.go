package srvmappers

import (
	"strconv"

	"github.com/docker/docker/api/types/mount"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type VolumesMapper struct{}

func (m *VolumesMapper) Map(service *compose.Service, live swarm.StackService) {
	if live.ServiceSpec.TaskTemplate.ContainerSpec == nil {
		return
	}

	service.Volumes = m.mapVolumes(live.ServiceSpec.TaskTemplate.ContainerSpec.Mounts)
}

func (m *VolumesMapper) mapVolumes(rawMounts []mount.Mount) compose.ServiceVolumes {
	if len(rawMounts) == 0 {
		return compose.ServiceVolumes{}
	}

	mapped := compose.ServiceVolumes{
		Volumes: make([]*compose.ServiceVolume, 0, len(rawMounts)),
		Map:     make(map[string]*compose.ServiceVolume, len(rawMounts)),
	}

	for _, rawMount := range rawMounts {
		volume := &compose.ServiceVolume{
			Type:        compose.ServiceVolumeType(rawMount.Type),
			Source:      rawMount.Source,
			Target:      rawMount.Target,
			ReadOnly:    rawMount.ReadOnly,
			Consistency: rawMount.Consistency,
		}

		if rawMount.BindOptions != nil {
			volume.Bind = &compose.ServiceVolumeBind{
				CreateHostPath: ptr(rawMount.BindOptions.CreateMountpoint),
				Propagation:    rawMount.BindOptions.Propagation,
			}
		}

		if rawMount.VolumeOptions != nil {
			volume.Volume = &compose.ServiceVolumeVolume{
				Nocopy:  rawMount.VolumeOptions.NoCopy,
				Subpath: rawMount.VolumeOptions.Subpath,
			}
		}

		if rawMount.TmpfsOptions != nil {
			volume.Tmpfs = &compose.ServiceVolumeTmpfs{
				Size: strconv.FormatInt(rawMount.TmpfsOptions.SizeBytes, 10),
				Mode: rawMount.TmpfsOptions.Mode,
			}
		}

		mapped.Volumes = append(mapped.Volumes, volume)
		mapped.Map[volume.Target] = volume
	}

	return mapped
}
