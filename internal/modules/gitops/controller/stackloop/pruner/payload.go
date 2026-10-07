package pruner

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type PruneServicesRequest struct {
	Stack   config.StackSpec
	Commit  string
	Desired []compose.Service
	Live    []swarm.StackService
}
