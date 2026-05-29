package tools

import (
	"context"
	"net"

	"github.com/swarm-deploy/swarm-deploy/internal/differ"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// HistoryReader reads current event history snapshot.
type HistoryReader interface {
	// List returns current event history snapshot.
	List() []history.Entry
}

// SyncTrigger triggers synchronization run.
type SyncTrigger interface {
	// Manual enqueues synchronization.
	Manual(ctx context.Context) bool
}

// NodesReader reads current Swarm nodes snapshot.
type NodesReader interface {
	// List returns current nodes snapshot.
	List() []swarm.Node
}

// NetworkReader reads current Docker networks snapshot.
type NetworkReader interface {
	// List returns current Docker networks snapshot.
	List(ctx context.Context) ([]swarm.Network, error)
}

// PluginReader reads current Docker plugins snapshot.
type PluginReader interface {
	// List returns current Docker plugins snapshot.
	List(ctx context.Context) ([]swarm.Plugin, error)
}

// SecretReader reads current Docker secrets snapshot.
type SecretReader interface {
	// List returns current Docker secrets snapshot.
	List(ctx context.Context) ([]swarm.Secret, error)
}

// ServicesReader reads current service metadata snapshot.
type ServicesReader interface {
	// List returns current services metadata snapshot.
	List() []service.Info
}

// DNSResolver resolves DNS names to IP addresses.
type DNSResolver interface {
	// LookupIPAddr resolves host and returns a list of addresses.
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// ImageVersionResolver resolves current image version in a container registry.
type ImageVersionResolver interface {
	// ResolveActualVersion resolves current image version in a container registry.
	ResolveActualVersion(ctx context.Context, image string) (registry.ImageVersion, error)
}

// GitRepository reads commit metadata and per-file diffs.
type GitRepository interface {
	// List returns latest commits from HEAD up to the provided limit.
	List(ctx context.Context, limit int) ([]gitx.CommitMeta, error)
	// Show returns commit metadata and per-file diff for a given commit hash.
	Show(ctx context.Context, commitHash string) (gitx.Commit, error)
}

// CommitDiffer compares old/new compose snapshots and returns semantic diff.
type CommitDiffer interface {
	// Compare returns changed services in old/new compose snapshots.
	Compare(composeFiles []differ.ComposeFile) (differ.Diff, error)
}
