package stackloop

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

type ReconciliationRequest struct {
	Stack      config.StackSpec
	PrevDigest string
	HasPrev    bool
	IsManual   bool
}

// ReconciliationResponse describes the stack reconciliation outcome.
type ReconciliationResponse struct {
	// SourceDigest is the digest of the source compose file before in-memory mutations.
	SourceDigest string
	// Services lists services defined in the reconciled compose file.
	Services []compose.Service
	// PrunedServices lists orphan services removed from the swarm stack.
	PrunedServices []string
	// Skipped reports whether deployment was skipped because the compose source digest was unchanged.
	Skipped bool
}
