package waiter

import (
	dockerswarm "github.com/docker/docker/api/types/swarm"
	swarmkitapi "github.com/moby/swarmkit/v2/api"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func evaluateTestService(
	name string,
	baseline swarm.StackService,
	service swarm.StackService,
	tasks []swarm.ServiceTask,
	unresolved bool,
	expected dockerswarm.ServiceSpec,
	placementNodes map[string]*swarmkitapi.Node,
) ServiceProgress {
	return evaluateService(name, baseline, service, tasks, unresolved, expected, placementNodes, services.UpdateGeneration{})
}
