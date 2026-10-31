//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mocks.go -package=swarm
package swarm

import (
	"context"

	dockerswarm "github.com/docker/docker/api/types/swarm"
)

type ServiceManager interface {
	// GetReplicas returns desired replicas count for a stack service.
	GetReplicas(ctx context.Context, serviceRef ServiceReference) (uint64, error)

	// ListServices returns Docker Swarm services matching the provided filter.
	ListServices(ctx context.Context, filter ListServicesFilter) ([]StackService, error)

	// Remove deletes service by Docker service identifier or full service name.
	Remove(ctx context.Context, serviceIDOrName string) error

	// Scale sets desired replicas count for a stack service.
	Scale(ctx context.Context, serviceRef ServiceReference, replicas uint64) error

	// ScaleByID scales a live service by Docker ID and returns its previous replica count.
	ScaleByID(ctx context.Context, serviceID string, replicas uint64) (ServiceScaleResult, error)

	// Restart restarts stack service by scaling replicas to zero and restoring previous count.
	Restart(ctx context.Context, serviceRef ServiceReference) (uint64, error)

	// GetStatus returns compact status snapshot for a stack service.
	GetStatus(ctx context.Context, serviceRef ServiceReference) (ServiceStatus, error)

	// ListTasks returns tasks for a Docker service identifier or full service name.
	ListTasks(ctx context.Context, serviceIDOrName string) ([]ServiceTask, error)

	// GetTask returns compact runtime details for a Docker task identifier.
	GetTask(ctx context.Context, taskID string) (ServiceTaskDetails, error)

	// Get returns full compact service projection for a stack service.
	Get(ctx context.Context, serviceRef ServiceReference) (Service, error)

	// Logs returns recent logs for a stack service.
	Logs(ctx context.Context, serviceRef ServiceReference, options ServiceLogsOptions) ([]string, error)

	// TaskLogs streams normalized logs for a Docker Swarm task.
	TaskLogs(ctx context.Context, taskID string, options TaskLogsOptions) (<-chan LogEntry, <-chan error, error)
}

type ImageManager interface {
	// Get returns compact image metadata by image reference.
	Get(ctx context.Context, imageRef string) (Image, error)
}

type SecretManager interface {
	// List returns Docker secrets matching the provided filter.
	List(ctx context.Context, filter ListSecretsFilter) ([]Secret, error)

	// Create creates a Docker secret and returns its identifier.
	Create(ctx context.Context, req CreateSecretRequest) (string, error)

	// UpdateLabels updates only a secret's mutable labels, preserving its other metadata.
	UpdateLabels(ctx context.Context, secretID string, labels map[string]string) error

	// Remove deletes a Docker secret by identifier.
	Remove(ctx context.Context, secretID string) error

	// ResolveReference resolves a secret reference by source and target.
	ResolveReference(ctx context.Context, source, target string) (*dockerswarm.SecretReference, error)
}

type ConfigManager interface {
	// Get returns Docker config metadata by name or ID.
	Get(ctx context.Context, configName string) (Config, error)

	// List returns Docker configs matching the provided filter.
	List(ctx context.Context, filter ListConfigsFilter) ([]Config, error)

	// Create creates a Docker config and returns its identifier.
	Create(ctx context.Context, req CreateConfigRequest) (string, error)

	// Remove deletes a Docker config by identifier.
	Remove(ctx context.Context, configID string) error

	// ResolveReference resolves Docker config reference by source and target.
	ResolveReference(ctx context.Context, source, target string) (*dockerswarm.ConfigReference, error)
}

type NetworkManager interface {
	// Get returns Docker network by name or identifier.
	Get(ctx context.Context, name string) (Network, error)

	// List returns Docker networks matching the provided filter.
	List(ctx context.Context, filter ListNetworksFilter) ([]Network, error)

	// Map returns Docker networks indexed by network identifier.
	Map(ctx context.Context, ids []string) (map[string]Network, error)

	// Create creates Docker network and returns its identifier.
	Create(ctx context.Context, req CreateNetworkRequest) (string, error)
}

type NodeManager interface {
	// List returns current Docker Swarm nodes snapshot.
	List(ctx context.Context) ([]Node, error)

	// SetLabel sets Docker node label value while preserving the rest of the node spec.
	SetLabel(ctx context.Context, req NodeLabelUpdateRequest) error

	// DeleteLabel removes Docker node label while preserving the rest of the node spec.
	DeleteLabel(ctx context.Context, nodeID string, key string) error
}
