package srvmappers

import (
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type SecretsMapper struct{}

func (m *SecretsMapper) Map(service *compose.Service, live swarm.StackService) {
	if live.ServiceSpec.TaskTemplate.ContainerSpec == nil {
		return
	}

	service.Secrets = m.mapSecrets(live.ServiceSpec.TaskTemplate.ContainerSpec.Secrets)
}

func (m *SecretsMapper) mapSecrets(rawRefs []*dockerswarm.SecretReference) []compose.ObjectRef {
	if len(rawRefs) == 0 {
		return nil
	}

	mapped := make([]compose.ObjectRef, 0, len(rawRefs))
	for _, rawRef := range rawRefs {
		if rawRef == nil {
			continue
		}

		ref := compose.ObjectRef{
			Source: buildObjectRefSource(rawRef.SecretName, rawRef.SecretID),
		}

		if rawRef.File != nil {
			ref.Target = rawRef.File.Name
			ref.Mode = ptr(rawRef.File.Mode)
			ref.Gid = rawRef.File.GID
			ref.Uid = rawRef.File.UID
		}

		mapped = append(mapped, ref)
	}

	if len(mapped) == 0 {
		return nil
	}

	return mapped
}
