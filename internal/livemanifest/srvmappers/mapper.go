package srvmappers

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type Mapper interface {
	Map(service *compose.Service, live swarm.StackService)
}

type ComposeMapper struct {
	mappers []Mapper
}

func NewComposeMapper(mappers ...Mapper) *ComposeMapper {
	return &ComposeMapper{
		mappers: mappers,
	}
}

func (m *ComposeMapper) Map(service *compose.Service, live swarm.StackService) {
	for _, mapper := range m.mappers {
		mapper.Map(service, live)
	}
}
