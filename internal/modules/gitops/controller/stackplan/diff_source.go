//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mock.go -package=stackplan
package stackplan

import (
	"context"

	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
)

// DiffSource returns files changed between two revisions.
type DiffSource interface {
	// Diff returns files changed between oldRevision and newRevision.
	Diff(ctx context.Context, oldRevision string, newRevision string) ([]gitx.CommitFileDiff, error)
}
