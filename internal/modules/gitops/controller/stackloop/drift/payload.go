package drift

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type AnalyzeRequest struct {
	Stack   config.StackSpec
	Desired compose.File
	Live    []swarm.StackService
}

type AnalyzeResponse struct {
	Drifts map[string]ServiceDrift
}

type ServiceDrift struct {
	ServiceName string

	Reason string

	ServiceMissed bool
}
