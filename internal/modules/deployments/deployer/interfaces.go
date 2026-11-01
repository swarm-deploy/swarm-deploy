//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mock.go -package=deployer

package deployer

import (
	"context"
	"time"

	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/initjob"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/secrets"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Result contains observable results produced while deploying one stack.
type Result struct {
	// Verification contains the known rollout obligations produced by execution.
	Verification deploymentmodel.Verification
	// InitJobs contains completed Init Job executions in execution order.
	InitJobs []InitJobResult
}

// InitJobStatus describes the outcome of one Init Job execution.
type (
	InitJobMetrics          = initjob.Metrics
	InitJobRunner           = initjob.InitJobRunner
	InitJobStatus           = initjob.Status
	InitJobResult           = initjob.Result
	InitJobSpec             = initjob.Spec
	JobFailedError          = initjob.JobFailedError
	ResolvedResource        = resources.ResolvedResource
	SecretDecoder           = secrets.SecretDecoder
	SecretResolver          = secrets.SecretResolver
	PlaintextDecoder        = secrets.PlaintextDecoder
	AgeDecoder              = secrets.AgeDecoder
	ServiceState            = waiter.ServiceState
	ServiceProgress         = waiter.ServiceProgress
	ServiceProgressCallback = waiter.ServiceProgressCallback
	ServiceWaitResult       = waiter.ServiceWaitResult
	ServiceWaitRequest      = waiter.ServiceWaitRequest
	ServiceWaiter           = waiter.ServiceWaiter
)

const (
	InitJobSucceeded   = initjob.Succeeded
	InitJobFailed      = initjob.Failed
	InitJobInterrupted = initjob.Interrupted
	ServicePending     = waiter.ServicePending
	ServiceReady       = waiter.ServiceReady
	ServiceFailed      = waiter.ServiceFailed
)

func NewSecretResolver(fileSystem fs.FileSystem, decoders ...SecretDecoder) *SecretResolver {
	return secrets.NewSecretResolver(fileSystem, decoders...)
}

func NewAgeDecoder(identity []byte) *AgeDecoder { return secrets.NewAgeDecoder(identity) }

func NewInitJobRunner(
	dockerClient *client.Client,
	swarmService *swarm.Swarm,
	pollInterval time.Duration,
	timeout time.Duration,
	metrics InitJobMetrics,
) *initjob.InitJobRunner {
	return initjob.NewInitJobRunner(dockerClient, swarmService, pollInterval, timeout, metrics)
}

func NewServiceWaiter(
	services swarm.ServiceManager,
	pollInterval time.Duration,
	timeout time.Duration,
) *ServiceWaiter {
	return waiter.NewServiceWaiter(services, pollInterval, timeout)
}

// StackOptions controls recovery during a deployment retry.
type StackOptions struct {
	// UnresolvedServices carries prior desired-state failures requiring verification.
	UnresolvedServices []string
	// RetryFailedUpdates restarts services whose accepted specification has a terminal update failure.
	RetryFailedUpdates bool
}

// StackRequest contains the effective stack and resource-resolution inputs.
type StackRequest struct {
	// StackName identifies the Docker stack namespace.
	StackName string
	// SourceComposePath is the original Compose path used to resolve resource files.
	SourceComposePath string
	// Desired is the prepared in-memory definition to apply.
	Desired compose.Compose
	// Options controls recovery of previous failed updates.
	Options StackOptions
}

// ServiceRequest contains one service remediation without stack-wide Init Jobs or pruning.
type ServiceRequest struct {
	// StackName identifies the Docker stack namespace.
	StackName string
	// SourceComposePath is the original Compose path used to resolve resource files.
	SourceComposePath string
	// ServiceName is the Compose service name without the stack prefix.
	ServiceName string
	// Desired is the prepared in-memory stack containing the service.
	Desired compose.Compose
}

// StackDeployer reconciles prepared desired state through native Docker API execution.
type StackDeployer interface {
	// DeployStack reconciles resources, runs Init Jobs, applies services and verifies convergence.
	DeployStack(ctx context.Context, request StackRequest) (Result, error)
	// DeployService reconciles resources and applies one service without Init Jobs or pruning.
	DeployService(ctx context.Context, request ServiceRequest) (Result, error)
}
