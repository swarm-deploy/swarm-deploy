package srvmappers

import (
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type Mapper interface {
	Map(service *compose.Service, live dockerswarm.ServiceSpec)
}

type ComposeMapper struct {
	mappers []Mapper
}

func NewComposeMapper(mappers ...Mapper) *ComposeMapper {
	return &ComposeMapper{
		mappers: mappers,
	}
}

func (m *ComposeMapper) Map(service *compose.Service, live dockerswarm.ServiceSpec) {
	for _, mapper := range m.mappers {
		mapper.Map(service, live)
	}
}
