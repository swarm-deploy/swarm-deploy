package inspector

import (
	"errors"
	"fmt"
	"time"

	"github.com/docker/docker/client"
)

var ErrServiceNotFound = errors.New("service not found")

// Inspector reads runtime service/container status from Docker API.
type Inspector struct {
	dockerClient *client.Client
}

// ServiceStatus contains compact status snapshot of a stack service.
type ServiceStatus struct {
	// Stack is a stack name.
	Stack string
	// Service is a service name without stack prefix.
	Service string
	// Spec contains current compact service spec snapshot.
	Spec ServiceSpec
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

// ServiceLabels contains labels from service, container and image inspect.
type ServiceLabels struct {
	// Service contains Docker service-level labels from annotations.
	Service map[string]string
	// Container contains container labels from task template.
	Container map[string]string
	// ContainerEnv contains environment variables from task container spec.
	ContainerEnv []string
	// Image contains OCI labels from image config.
	Image map[string]string
}

// New creates swarm inspector.
func New() (*Inspector, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker api client: %w", err)
	}

	return &Inspector{
		dockerClient: cli,
	}, nil
}
