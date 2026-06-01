package stackloop

import "github.com/swarm-deploy/swarm-deploy/internal/config"

type ReconciliationRequest struct {
	Stack      config.StackSpec
	PrevDigest string
	HasPrev    bool
	IsManual   bool
}
