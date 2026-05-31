package drift

import (
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type EnvComparator struct{}

func (e *EnvComparator) Compare(desired compose.Service, live dockerswarm.ServiceSpec, drift *Drift) error {
	if desired.Environment.IsEmpty() && len(live.TaskTemplate.ContainerSpec.Env) == 0 {
		return nil
	}

	liveEnv, err := compose.NewEnvironment(live.TaskTemplate.ContainerSpec.Env)
	if err != nil {
		return fmt.Errorf("parse live environment: %w", err)
	}

	redundant := liveEnv.Clone().Map

	for key := range desired.Environment.Map {
		if liveEnv.Has(key) {
			continue
		}

		drift.Env.Missed = append(drift.Env.Missed, key)
		delete(redundant, key)
	}

	drift.Env.Redundant = make([]string, 0, len(redundant))

	for key := range redundant {
		drift.Env.Redundant = append(drift.Env.Redundant, key)
	}

	drift.Env.OutOfSync = len(drift.Env.Redundant) > 0 || len(drift.Env.Missed) > 0

	return nil
}
