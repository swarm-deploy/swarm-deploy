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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/stack"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Result contains observable results produced while deploying one stack.
type Result struct {
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
	Runner                  = stack.Runner
	ServiceState            = waiter.ServiceState
	ServiceProgress         = waiter.ServiceProgress
	ServiceProgressCallback = waiter.ServiceProgressCallback
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

// StackDeployer reconciles one stack through native Docker API execution.
type StackDeployer interface {
	// DeployStack reconciles resources from sourceComposePath and applies the prepared desired stack.
	DeployStack(
		ctx context.Context,
		stackName,
		sourceComposePath,
		deployComposePath string,
		desired compose.Compose,
	) (Result, error)
	// DeployService reconciles resources and applies one service without pruning the rest of the stack.
	DeployService(
		ctx context.Context,
		stackName,
		sourceComposePath,
		serviceName string,
		desired,
		deploy compose.Compose,
	) (Result, error)
}
