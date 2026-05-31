package srvmappers

import (
	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type VolumesMapper struct{}

func (m *VolumesMapper) Map(service *compose.Service, live dockerswarm.ServiceSpec) {
	if live.TaskTemplate.ContainerSpec == nil {
		return
	}

	service.Volumes = m.mapVolumes(live.TaskTemplate.ContainerSpec.Mounts)
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
			Type:     compose.ServiceVolumeType(rawMount.Type),
			Source:   rawMount.Source,
			Target:   rawMount.Target,
			ReadOnly: rawMount.ReadOnly,
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

		mapped.Volumes = append(mapped.Volumes, volume)
		mapped.Map[volume.Target] = volume
	}

	return mapped
}

func ptr[t any](v t) *t {
	return &v
}
