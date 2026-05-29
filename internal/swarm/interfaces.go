//go:generate mockgen -source=$GOFILE -destination=mocks.go -package=swarm
package swarm

import "context"

type ServiceManager interface {
	// GetReplicas returns desired replicas count for a stack service.
	GetReplicas(ctx context.Context, serviceRef ServiceReference) (uint64, error)

	// ListStackServices returns services currently attached to provided stack.
	ListStackServices(ctx context.Context, stackName string) ([]StackService, error)

	// Remove deletes service by Docker service identifier or full service name.
	Remove(ctx context.Context, serviceIDOrName string) error

	// Scale sets desired replicas count for a stack service.
	Scale(ctx context.Context, serviceRef ServiceReference, replicas uint64) error

	// Restart restarts stack service by scaling replicas to zero and restoring previous count.
	Restart(ctx context.Context, serviceRef ServiceReference) (uint64, error)

	// GetStatus returns compact status snapshot for a stack service.
	GetStatus(ctx context.Context, serviceRef ServiceReference) (ServiceStatus, error)

	// ListTasks returns service tasks for realtime container status rendering.
	ListTasks(ctx context.Context, serviceRef ServiceReference) ([]ServiceTask, error)

	// Get returns full compact service projection for a stack service.
	Get(ctx context.Context, serviceRef ServiceReference) (Service, error)

	// Labels returns service, container and image labels for a stack service.
	Labels(ctx context.Context, serviceRef ServiceReference) (ServiceLabels, error)

	// Logs returns recent logs for a stack service.
	Logs(ctx context.Context, serviceRef ServiceReference, options ServiceLogsOptions) ([]string, error)
}
