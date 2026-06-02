package pruner

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

type PruneServicesRequest struct {
	Stack   config.StackSpec
	Desired []compose.Service
}
