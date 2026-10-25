package deployer

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const deployArgsExtraCount = 3

type Deployer struct {
	stackDeployArgs []string
	runner          Runner
	resources       *resourceReconciler
	secretResolver  *SecretResolver

	initJobRunner initJobExecutor
}

type InitJobMetrics interface {
	// RecordInitJobRun records one init job run by stack and service.
	RecordInitJobRun(stack, service string)
}

type initJobExecutor interface {
	// Run executes one init job based on deployment context and job spec.
	Run(ctx context.Context, spec InitJobSpec) (deploymentmodel.InitJobExecution, error)
}

type InitJobSpec struct {
	// StackName is a stack where init job service is created.
	StackName string
	// ServiceName is a parent service name that owns init job declaration.
	ServiceName string
	// DefaultNetwork is a fallback list of networks from parent service.
	DefaultNetwork []string
	// ServiceSecrets is a list of parent service secret references.
	ServiceSecrets []compose.ObjectRef
	// ServiceConfigs is a list of parent service config references.
	ServiceConfigs []compose.ObjectRef
	// ResolvedSecrets contains Docker resources indexed by Compose aliases.
	ResolvedSecrets map[string]ResolvedResource
	// ResolvedConfigs contains Docker resources indexed by Compose aliases.
	ResolvedConfigs map[string]ResolvedResource
	// Job is a source compose init job specification.
	Job compose.InitJob
}

// ResolvedResource identifies an existing Docker Swarm config or secret.
type ResolvedResource struct {
	// ID is the Docker resource identifier.
	ID string
	// Name is the Docker resource name.
	Name string
}

func NewDeployer(
	initJobPoll time.Duration,
	initJobTimeout time.Duration,
	dockerClient *client.Client,
	swarmService *swarm.Swarm,
	initJobMetrics InitJobMetrics,
	secretResolver *SecretResolver,
) StackDeployer {
	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy", "--with-registry-auth", "--detach=false", "--quiet"},
		runner:          swarmService.BinaryRunner,
		secretResolver:  secretResolver,
		resources:       newResourceReconciler(swarmService),
		initJobRunner: NewInitJobRunner(
			dockerClient,
			swarmService,
			initJobPoll,
			initJobTimeout,
			initJobMetrics,
		),
	}

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return deployer
	}

	return newTraceableDeployer(tp, deployer)
}

const binaryTimeout = 2 * time.Minute

func (d *Deployer) DeployStack(
	ctx context.Context,
	stackName,
	sourceComposePath,
	deployComposePath string,
	desired compose.Compose,
	options DeployOptions,
) error {
	resolved := resolvedResources{}
	if len(desired.Configs) > 0 || len(desired.Secrets) > 0 {
		data, err := d.secretResolver.Resolve(ctx, stackName, sourceComposePath, desired.Secrets)
		if err != nil {
			return fmt.Errorf("resolve secrets: %w", err)
		}
		resolved, err = d.resources.Reconcile(ctx, stackName, desired.Configs, desired.Secrets, data)
		clearSecretData(data)
		if err != nil {
			return fmt.Errorf("reconcile configs and secrets: %w", err)
		}
	}

	if err := d.runInitJobs(ctx, stackName, desired.Services, resolved, options.InitJobFinished); err != nil {
		return err
	}

	args := make([]string, 0, len(d.stackDeployArgs)+deployArgsExtraCount)
	args = append(args, d.stackDeployArgs...)
	args = append(args, "-c", deployComposePath, stackName)

	ctx, cancel := context.WithTimeout(ctx, binaryTimeout)
	defer cancel()

	if _, err := d.runner.Run(ctx, args...); err != nil {
		return fmt.Errorf("deploy stack %s: %w", stackName, err)
	}

	return nil
}

func (d *Deployer) runInitJobs(
	ctx context.Context,
	stackName string,
	services []compose.Service,
	resolved resolvedResources,
	onFinished func(context.Context, deploymentmodel.InitJobExecution),
) error {
	ordinal := 0
	for _, service := range services {
		// Jobs are run in declaration order per service to keep behavior deterministic.
		for _, job := range service.InitJobs {
			execution, err := d.initJobRunner.Run(ctx, InitJobSpec{
				StackName:       stackName,
				ServiceName:     service.Name,
				DefaultNetwork:  service.Networks.GetNames(),
				ServiceSecrets:  service.Secrets,
				ServiceConfigs:  service.Configs,
				ResolvedSecrets: resolved.secrets,
				ResolvedConfigs: resolved.configs,
				Job:             job,
			})
			if !execution.StartedAt.IsZero() {
				ordinal++
				execution.Ordinal = ordinal
				if onFinished != nil {
					onFinished(ctx, execution)
				}
			}
			if err != nil {
				return fmt.Errorf("service %s init job %s: %w", service.Name, job.Name, err)
			}
		}
	}
	return nil
}
