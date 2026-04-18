package tools

import (
	"context"
	"net"

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
	// Manual enqueues synchronization.
	Manual(ctx context.Context) bool
}

// NodesReader reads current Swarm nodes snapshot.
type NodesReader interface {
	// List returns current nodes snapshot.
	List() []inspector.NodeInfo
}

// NetworkInspector inspects current Docker networks snapshot.
type NetworkInspector interface {
	// InspectNetworks returns current Docker networks snapshot.
	InspectNetworks(ctx context.Context) ([]inspector.NetworkInfo, error)
}

// PluginInspector inspects current Docker plugins snapshot.
type PluginInspector interface {
	// InspectPlugins returns current Docker plugins snapshot.
	InspectPlugins(ctx context.Context) ([]inspector.PluginInfo, error)
}

// SecretInspector inspects current Docker secrets snapshot.
type SecretInspector interface {
	// InspectSecrets returns current Docker secrets snapshot.
	InspectSecrets(ctx context.Context) ([]inspector.SecretInfo, error)
}

// ServiceLogsInspector reads logs of a specific stack service.
type ServiceLogsInspector interface {
	// InspectServiceLogs returns recent log lines for the given stack service.
	InspectServiceLogs(
		ctx context.Context,
		stackName string,
		serviceName string,
		options inspector.ServiceLogsOptions,
	) ([]string, error)
}

// ServiceSpecInspector reads compact service spec snapshot for a stack service.
type ServiceSpecInspector interface {
	// InspectServiceSpec returns compact service projection for the given stack service.
	InspectServiceSpec(ctx context.Context, stackName string, serviceName string) (inspector.Service, error)
}

// ServicesReader reads current service metadata snapshot.
type ServicesReader interface {
	// List returns current services metadata snapshot.
	List() []service.Info
}

// ServiceReplicasManager manages replicas for stack services.
type ServiceReplicasManager interface {
	// InspectServiceReplicas returns current desired service replicas count.
	InspectServiceReplicas(ctx context.Context, stackName, serviceName string) (uint64, error)
	// UpdateServiceReplicas sets desired service replicas count.
	UpdateServiceReplicas(ctx context.Context, stackName, serviceName string, replicas uint64) error
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
