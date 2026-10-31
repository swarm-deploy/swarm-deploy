package deployer

import (
	"context"
	"sync"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/initjob"
	stackpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/stack"
	waiterpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const deployerTracerName = "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"

const (
	binaryTimeout             = 2 * time.Minute
	defaultServiceWaitTimeout = 10 * time.Minute
)

type initJobExecutor interface {
	Run(ctx context.Context, spec initjob.Spec) (initjob.Result, error)
}

type serviceWaitExecutor interface {
	Snapshot(ctx context.Context, stackName string) ([]swarm.StackService, error)
	Wait(ctx context.Context, request waiterpkg.ServiceWaitRequest) error
}

// Deployer coordinates deployment execution components through two explicit pipelines.
type Deployer struct {
	// Compatibility fields remain injectable for existing constructor and package tests.
	stackDeployArgs []string
	runner          Runner
	resources       *resourceReconciler
	secretResolver  *SecretResolver
	initJobRunner   initJobExecutor
	serviceWaiter   serviceWaitExecutor

	stackApplier      *stackpkg.Applier
	deployStackPipe   *pipe.Pipeline[*deployStackPayload]
	deployServicePipe *pipe.Pipeline[*deployServicePayload]
	pipelineOnce      sync.Once
}

// NewDeployer builds the deployment execution components and their pipelines.
func NewDeployer(
	initJobPoll time.Duration,
	initJobTimeout time.Duration,
	dockerClient *client.Client,
	swarmService *swarm.Swarm,
	initJobMetrics InitJobMetrics,
	secretResolver *SecretResolver,
) StackDeployer {
	d := &Deployer{
		stackDeployArgs: defaultStackDeployArgs(),
		runner:          swarmService.BinaryRunner,
		secretResolver:  secretResolver,
		resources:       newResourceReconciler(swarmService),
		initJobRunner: initjob.NewInitJobRunner(
			dockerClient,
			swarmService,
			initJobPoll,
			initJobTimeout,
			initJobMetrics,
		),
		serviceWaiter: waiterpkg.NewServiceWaiter(
			swarmService.Services,
			initJobPoll,
			defaultServiceWaitTimeout,
		),
	}
	d.attachPipelines()
	return d
}

func defaultStackDeployArgs() []string { return stackpkg.DefaultDeployArgs() }

// DeployStack runs the full stack deployment pipeline.
func (d *Deployer) DeployStack(
	ctx context.Context,
	stackName,
	sourceComposePath,
	deployComposePath string,
	desired compose.Compose,
) (Result, error) {
	d.attachPipelines()
	payload := &deployStackPayload{
		stackName:         stackName,
		sourceComposePath: sourceComposePath,
		deployComposePath: deployComposePath,
		desired:           desired,
	}

	ctx, span := otel.GetTracerProvider().Tracer(deployerTracerName).Start(
		ctx, deployStackOperationName, oteltrace.WithAttributes(stackTraceAttributes(payload)...),
	)
	defer span.End()

	err := d.deployStackPipe.Run(ctx, payload)
	if err != nil {
		tracing.FailSpan(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	return payload.result, err
}

// DeployService runs the single-service deployment pipeline without Init Jobs or pruning.
func (d *Deployer) DeployService(
	ctx context.Context,
	stackName,
	sourceComposePath,
	serviceName string,
	desired,
	deploy compose.Compose,
) (Result, error) {
	d.attachPipelines()
	payload := &deployServicePayload{
		stackName:         stackName,
		sourceComposePath: sourceComposePath,
		serviceName:       serviceName,
		desired:           desired,
		deploy:            deploy,
	}
	defer payload.cleanup()

	ctx, span := otel.GetTracerProvider().Tracer(deployerTracerName).Start(
		ctx, deployServiceOperationName, oteltrace.WithAttributes(serviceTraceAttributes(payload)...),
	)
	defer span.End()

	err := d.deployServicePipe.Run(ctx, payload)
	if err != nil {
		tracing.FailSpan(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	return payload.result, err
}
