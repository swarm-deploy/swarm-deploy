package tools

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/differ"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
)

// HistoryReader reads current event history snapshot.
type HistoryReader interface {
	// List returns current event history snapshot.
	List() []history.Entry
}

// SyncTrigger triggers synchronization run.
type SyncTrigger interface {
	// Trigger enqueues synchronization by reason.
	Trigger(reason controller.TriggerReason) bool
}

// NodesReader reads current Swarm nodes snapshot.
type NodesReader interface {
	// List returns current nodes snapshot.
	List() []inspector.NodeInfo
}

// ServicesReader reads current service metadata snapshot.
type ServicesReader interface {
	// List returns current services metadata snapshot.
	List() []service.Info
}

// ImageVersionResolver resolves current image version in a container registry.
type ImageVersionResolver interface {
	// ResolveActualVersion resolves current image version in a container registry.
	ResolveActualVersion(ctx context.Context, image string) (registry.ImageVersion, error)
}

// GitRepository reads commit metadata and per-file diffs.
type GitRepository interface {
	// Show returns commit metadata and per-file diff for a given commit hash.
	Show(ctx context.Context, commitHash string) (gitx.Commit, error)
}

// CommitDiffer compares old/new compose snapshots and returns semantic diff.
type CommitDiffer interface {
	// Compare returns changed services in old/new compose snapshots.
	Compare(composeFiles []differ.ComposeFile) (differ.Diff, error)
}
