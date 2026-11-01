package swarm

import (
	"errors"
	"fmt"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
)

// ErrServiceNotFound means that service does not exist in swarm.
var ErrServiceNotFound = errors.New("service not found")

// ErrServiceModeUnsupported means scaling is unavailable for this service mode.
var ErrServiceModeUnsupported = errors.New("only replicated services can be scaled")

// ErrServiceUpdateConflict means the service changed during an update.
var ErrServiceUpdateConflict = errors.New("service changed while updating replicas; refresh and retry")

// ServiceScaleResult describes the service and replica count before a successful scale.
type ServiceScaleResult struct {
	// Stack is the Docker stack namespace, or empty for standalone services.
	Stack string
	// Service is the service name without the stack prefix.
	Service string
	// PreviousReplicas is the configured count read before the update.
	PreviousReplicas uint64
}

// ErrServiceConflict means that a service create or update conflicted with concurrent Docker state.
var ErrServiceConflict = errors.New("service conflict")

// ErrTaskNotFound means that task does not exist in swarm.
var ErrTaskNotFound = errors.New("task not found")

// ListServicesFilter contains Docker Swarm service filters.
type ListServicesFilter struct {
	// Stack limits services to the Docker stack namespace when specified.
	Stack string
}

// ServiceStatus contains compact status snapshot of a stack service.
type ServiceStatus struct {
	// Stack is a stack name.
	Stack string
	// Service is a service name without stack prefix.
	Service string
	// Spec contains current compact service spec snapshot.
	Spec ServiceSpec
	// ContainerLabels contains task container labels from service spec.
	ContainerLabels map[string]string
	// ContainerEnv contains task container environment variables from service spec.
	ContainerEnv []string
	// ContainerConfigs contains task container configs from service spec.
	ContainerConfigs []ServiceConfig
}

// ServiceTask contains compact realtime task data for service container status.
type ServiceTask struct {
	// ID is a Docker task identifier.
	ID string
	// Node is a task node identifier.
	Node string
	// Slot is the task slot within a replicated service.
	Slot int
	// DesiredState is the desired task state.
	DesiredState TaskState
	// CreatedAt is task creation timestamp from Docker Swarm.
	CreatedAt time.Time
	// UpdatedAt is task last update timestamp from Docker Swarm.
	UpdatedAt time.Time
	// CurrentState is the current task state.
	CurrentState TaskState
	// ContainerID is the task container identifier when assigned.
	ContainerID string
	// ExitCode is the task container exit code when container status is available.
	ExitCode *int
	// Error is a task runtime error.
	Error string
}

// TaskResources contains compact CPU and memory resource settings.
type TaskResources struct {
	// NanoCPUs is the configured CPU amount in nano-CPUs.
	NanoCPUs int64
	// MemoryBytes is the configured memory amount in bytes.
	MemoryBytes int64
}

// ServiceTaskDetails contains compact runtime details from an inspected Docker task.
type ServiceTaskDetails struct {
	// ID is a Docker task identifier.
	ID string
	// ServiceID is the Docker service identifier owning the task.
	ServiceID string
	// NodeID is the Docker node identifier assigned to the task.
	NodeID string
	// Slot is the task slot within a replicated service.
	Slot int
	// DesiredState is the desired task state.
	DesiredState TaskState
	// CurrentState is the current task state.
	CurrentState TaskState
	// Error is a task runtime error.
	Error string
	// CreatedAt is task creation timestamp from Docker Swarm.
	CreatedAt time.Time
	// UpdatedAt is task last update timestamp from Docker Swarm.
	UpdatedAt time.Time
	// ContainerID is the task container identifier when assigned.
	ContainerID string
	// ExitCode is the task container exit code when container status is available.
	ExitCode *int
	// Image is the container image captured in the inspected task specification.
	Image string
	// Environment contains original container environment entries from the inspected task.
	Environment []string
	// Reservations contains requested task resources when configured.
	Reservations *TaskResources
	// Limits contains task resource limits when configured.
	Limits *TaskResources
}

// ServiceSpec is a compact service spec projection.
type ServiceSpec struct {
	// Image is a full image reference configured for the service.
	Image string `json:"image"`
	// Mode is a service deploy mode (for example: replicated, global).
	Mode string `json:"mode"`
	// Replicas is desired replicas count for replicated mode.
	Replicas uint64 `json:"replicas"`
	// RequestedRAMBytes is a requested memory reservation in bytes.
	RequestedRAMBytes int64 `json:"requested_ram_bytes"`
	// RequestedCPUNano is a requested CPU reservation in nano-CPUs.
	RequestedCPUNano int64 `json:"requested_cpu_nano"`
	// LimitRAMBytes is a memory limit in bytes.
	LimitRAMBytes int64 `json:"limit_ram_bytes"`
	// LimitCPUNano is a CPU limit in nano-CPUs.
	LimitCPUNano int64 `json:"limit_cpu_nano"`
	// Labels contains service labels from service annotations.
	Labels map[string]string `json:"labels,omitempty"`
	// Secrets contains compact secret references from service spec.
	Secrets []ServiceSecret `json:"secrets,omitempty"`
	// Network contains compact network attachments from service spec.
	Network []ServiceNetwork `json:"network,omitempty"`
	// Configs contains compact config references from service spec.
	Configs []ServiceConfig `json:"configs,omitempty"`
}

// ServiceSecret is a compact service secret reference.
type ServiceSecret struct {
	// SecretID is a Docker secret identifier.
	SecretID string `json:"secret_id,omitempty"`
	// SecretName is a Docker secret name.
	SecretName string `json:"secret_name"`
	// Target is a target file path inside container.
	Target string `json:"target,omitempty"`
}

// ServiceNetwork is a compact service network attachment.
type ServiceNetwork struct {
	// Target is a Docker network identifier configured for the service.
	Target string `json:"target"`
	// Aliases contains DNS aliases configured for this network attachment.
	Aliases []string `json:"aliases,omitempty"`
}

// ServiceUpdateStatus is a compact projection of service rolling update state.
type ServiceUpdateStatus struct {
	// State is an update status state.
	State string `json:"state"`
	// StartedAt is update start time.
	StartedAt time.Time `json:"started_at,omitempty"`
	// CompletedAt is update completion time.
	CompletedAt time.Time `json:"completed_at,omitempty"`
	// Message is status details from swarm manager.
	Message string `json:"message,omitempty"`
}

// Service contains full service metadata with current and previous compact spec.
type Service struct {
	// ID is a Docker service identifier.
	ID string `json:"id"`
	// CreatedAt is service creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is service update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
	// Secrets contains effective secret references of current service spec.
	Secrets []ServiceSecret `json:"secrets,omitempty"`
	// Spec contains current compact service spec.
	Spec ServiceSpec `json:"spec"`
	// PreviousSpec contains previous service spec before last update.
	PreviousSpec *ServiceSpec `json:"previous_spec,omitempty"`
	// UpdateStatus contains current rolling update status when available.
	UpdateStatus *ServiceUpdateStatus `json:"update_status,omitempty"`
}

// ServiceLogsOptions configures stack service logs query.
type ServiceLogsOptions struct {
	// Limit is max number of latest lines to return.
	Limit int
	// Since defines lower bound for log timestamps.
	Since *time.Time
	// Until defines upper bound for log timestamps.
	Until *time.Time
}

// TaskLogsOptions configures task logs stream query.
type TaskLogsOptions struct {
	// Follow keeps the task logs stream open for new log lines.
	Follow bool
	// Limit is max number of latest lines to return before following.
	Limit int
	// Since defines an optional inclusive lower bound for Docker log timestamps.
	Since *time.Time
	// Until defines an optional inclusive upper bound for Docker log timestamps.
	Until *time.Time
}

// LogEntry contains one normalized Docker log line.
type LogEntry struct {
	// Timestamp is a Docker log timestamp parsed from the log line.
	Timestamp time.Time
	// Stream is stdout or stderr.
	Stream string
	// Message is log line payload without Docker timestamp.
	Message string
}

// StackService is a compact snapshot of a service belonging to a stack.
type StackService struct {
	// CompletedTasks is the count completed in the current job iteration.
	CompletedTasks uint64
	// JobStatus identifies the current execution of a Swarm job.
	JobStatus *dockerswarm.JobStatus

	// ID is a Docker service identifier.
	ID string
	// Name is a service name without stack prefix.
	Name string
	// Stack is the Docker stack namespace when the service belongs to a stack.
	Stack string
	// FullName is a Docker service name formatted as "<stack>_<service>".
	FullName string
	// Image is a container image reference configured for the service.
	Image string
	// Mode is a service deploy mode (for example: replicated, global).
	Mode string
	// Replicas is desired replicas count for replicated mode.
	Replicas *uint64
	// RunningTasks is the number of tasks currently running according to Docker.
	RunningTasks uint64
	// DesiredTasks is the number of tasks desired according to Docker.
	DesiredTasks uint64
	// CreatedAt is service creation timestamp.
	CreatedAt time.Time
	// UpdatedAt is service update timestamp.
	UpdatedAt time.Time
	// UpdateStatus contains current rolling update status when available.
	UpdateStatus *ServiceUpdateStatus
	// ServiceSpec is raw Docker service specification snapshot.
	ServiceSpec dockerswarm.ServiceSpec
	// PreviousSpec is the previous raw Docker service specification used by Swarm rollback.
	PreviousSpec *dockerswarm.ServiceSpec
	// Labels contains Docker service annotations labels.
	Labels map[string]string
}

type ServiceReference struct {
	stackName   string
	serviceName string

	fullName string
}

// NewServiceReference creates a service reference from stack and service names.
func NewServiceReference(stackName, serviceName string) ServiceReference {
	return ServiceReference{
		stackName:   stackName,
		serviceName: serviceName,
		fullName:    fmt.Sprintf("%s_%s", stackName, serviceName),
	}
}

// StackName returns stack name part.
func (r ServiceReference) StackName() string {
	return r.stackName
}

// ServiceName returns service name part.
func (r ServiceReference) ServiceName() string {
	return r.serviceName
}

// Name returns full service name as "<stack>_<service>".
func (r ServiceReference) Name() string {
	return r.fullName
}
