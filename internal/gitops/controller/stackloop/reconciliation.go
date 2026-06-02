package stackloop

import "github.com/swarm-deploy/swarm-deploy/internal/config"

type ReconciliationRequest struct {
	// Stack is the desired stack specification to reconcile.
	Stack config.StackSpec
	// Commit is the git revision associated with this reconciliation attempt.
	Commit string
	// IsManual reports whether reconciliation was triggered manually.
	IsManual bool
}
