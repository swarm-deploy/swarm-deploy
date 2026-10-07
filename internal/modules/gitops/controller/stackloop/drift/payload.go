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
	// ServiceName is the service with detected drift.
	ServiceName string

	// Reason describes the detected drift.
	Reason string

	// ServiceMissed is true when the service is absent from the live cluster.
	ServiceMissed bool

	// ServiceReplicasDiverged is true when live and desired replica counts differ.
	ServiceReplicasDiverged bool
}
