package srvmappers

import (
	"strings"

	container "github.com/docker/docker/api/types/container"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type HealthcheckMapper struct{}

func (m *HealthcheckMapper) Map(service *compose.Service, live swarm.StackService) {
	if live.ServiceSpec.TaskTemplate.ContainerSpec == nil {
		return
	}

	service.Healthcheck = m.mapHealthcheck(live.ServiceSpec.TaskTemplate.ContainerSpec.Healthcheck)
}

func (m *HealthcheckMapper) mapHealthcheck(healthcheck *container.HealthConfig) *compose.ServiceHealth {
	if healthcheck == nil {
		return nil
	}

	mapped := &compose.ServiceHealth{}
	hasData := false

	if len(healthcheck.Test) == 1 && strings.EqualFold(healthcheck.Test[0], "NONE") {
		mapped.Disable = true
		hasData = true
	} else if len(healthcheck.Test) > 0 {
		mapped.Test = compose.NewCommand(healthcheck.Test)
		hasData = true
	}

	if healthcheck.Interval > 0 {
		mapped.Interval = healthcheck.Interval.String()
		hasData = true
	}

	if healthcheck.Timeout > 0 {
		mapped.Timeout = healthcheck.Timeout.String()
		hasData = true
	}

	if healthcheck.StartPeriod > 0 {
		mapped.StartPeriod = healthcheck.StartPeriod.String()
		hasData = true
	}

	if healthcheck.StartInterval > 0 {
		mapped.StartInterval = healthcheck.StartInterval.String()
		hasData = true
	}

	if healthcheck.Retries > 0 {
		retries := uint64(healthcheck.Retries)
		mapped.Retries = &retries
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}
