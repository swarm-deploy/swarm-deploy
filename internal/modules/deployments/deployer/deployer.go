package deployer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/initjob"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/networks"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	waiterpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const deployerTracerName = "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"

const (
	serviceApplyTimeout       = 15 * time.Minute
	defaultServiceWaitTimeout = 10 * time.Minute
)

type initJobExecutor interface {
	// Run executes one ephemeral Init Job and returns its observed outcome.
	Run(ctx context.Context, spec initjob.Spec) (initjob.Result, error)
}

type serviceWaitExecutor interface {
	// Snapshot captures the stack state immediately before application.
	Snapshot(ctx context.Context, stackName string) ([]swarm.StackService, error)
	// Wait returns unresolved targets independently of optional progress notifications.
	Wait(ctx context.Context, request waiterpkg.ServiceWaitRequest) (waiterpkg.ServiceWaitResult, error)
}

// Deployer coordinates deployment execution components through two explicit pipelines.
type Deployer struct {
	resources      *resourceReconciler
	secretResolver *SecretResolver
	initJobRunner  initJobExecutor
	serviceWaiter  serviceWaitExecutor
	converter      *converter.Converter
	networks       *networks.Reconciler
	serviceApplier *services.Applier

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
		secretResolver: secretResolver,
		resources:      newResourceReconciler(swarmService),
		initJobRunner: initjob.NewInitJobRunner(
			dockerClient,
			swarmService,
			initJobPoll,
			initJobTimeout,
			initJobMetrics,
		),
		serviceWaiter: waiterpkg.NewServiceWaiter(
			swarmService.Services,
			swarmService.Nodes,
			initJobPoll,
			defaultServiceWaitTimeout,
		),
		converter:      converter.New(swarmService.Networks, swarmService.Configs, swarmService.Secrets),
		networks:       networks.New(swarmService.Networks),
		serviceApplier: services.New(swarmService.Services, registry.NewAuthManager()),
	}
	d.attachPipelines()
	return d
}

// RefreshRegistryAuth converts accepted desired state read-only and refreshes only matching live services.
// It does not reconcile resources, run Init Jobs, or create deployment verification obligations.
func (d *Deployer) RefreshRegistryAuth(ctx context.Context, request StackRequest) error {
	ctx, cancel := context.WithTimeout(ctx, serviceApplyTimeout)
	defer cancel()
	ctx, span := otel.GetTracerProvider().Tracer(deployerTracerName).Start(
		ctx, "deployer.RefreshRegistryAuth", oteltrace.WithAttributes(
			tracing.ResourceStackName.String(request.StackName),
		),
	)
	defer span.End()
	converted, err := d.converter.Convert(ctx, request.StackName, request.Desired)
	if err != nil {
		err = fmt.Errorf("convert registry authentication targets: %w", err)
	} else {
		err = d.serviceApplier.RefreshRegistryAuth(ctx, request.StackName, converted.Services)
	}
	if err != nil {
		// Converter diagnostics are sanitized, so retain the maintenance context's
		// cancellation classification without exposing the original API request.
		err = safePipelineError(errors.Join(err, ctx.Err()))
		tracing.FailSpan(span, err)
	}
	return err
}

// DeployStack runs the full stack deployment pipeline.
func (d *Deployer) DeployStack(ctx context.Context, request StackRequest) (Result, error) {
	d.attachPipelines()
	payload := &deployStackPayload{
		stackName: request.StackName, sourceComposePath: request.SourceComposePath, desired: request.Desired,
		result:             Result{Verification: deploymentmodel.Verification{Known: true}},
		retryFailedUpdates: request.Options.RetryFailedUpdates,
		unresolvedServices: request.Options.UnresolvedServices,
		beforeUpdate:       request.BeforeUpdate,
		applyPayload:       applyPayload{updateGenerations: maps.Clone(request.Options.UpdateGenerations)},
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
func (d *Deployer) DeployService(ctx context.Context, request ServiceRequest) (Result, error) {
	d.attachPipelines()
	payload := &deployServicePayload{
		stackName: request.StackName, sourceComposePath: request.SourceComposePath,
		serviceName: request.ServiceName, desired: request.Desired,
	}

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
