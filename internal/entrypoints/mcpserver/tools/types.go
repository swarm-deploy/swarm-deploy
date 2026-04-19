package tools

import (
	"context"
	"net"

	"github.com/artarts36/swarm-deploy/internal/differ"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
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
	List() []inspector.NodeInfo
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

// ServiceLogsInspector reads logs of a specific stack service.
type ServiceLogsInspector interface {
	// InspectServiceLogs returns recent log lines for the given stack service.
	Logs(
		ctx context.Context,
		stackName string,
		serviceName string,
		options swarm.ServiceLogsOptions,
	) ([]string, error)
}

// ServiceSpecInspector reads compact service spec snapshot for a stack service.
type ServiceSpecInspector interface {
	// InspectServiceSpec returns compact service projection for the given stack service.
	Get(ctx context.Context, stackName string, serviceName string) (swarm.Service, error)
}

// ServicesReader reads current service metadata snapshot.
type ServicesReader interface {
	// List returns current services metadata snapshot.
	List() []service.Info
}

// ServiceReplicasManager manages replicas for stack services.
type ServiceReplicasManager interface {
	// GetReplicas returns current desired service replicas count.
	GetReplicas(ctx context.Context, serviceRef swarm.ServiceReference) (uint64, error)
	// Scale sets desired service replicas count.
	Scale(ctx context.Context, serviceRef swarm.ServiceReference, replicas uint64) error
	// Restart restarts service by scaling replicas to zero and restoring previous count.
	// Returned value is the replicas count restored after restart.
	Restart(ctx context.Context, serviceRef swarm.ServiceReference) (uint64, error)
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
