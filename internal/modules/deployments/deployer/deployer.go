package deployer

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.yaml.in/yaml/v3"
)

const deployArgsExtraCount = 3

type Deployer struct {
	stackDeployArgs []string
	runner          Runner
	resources       *resourceReconciler
	secretResolver  *SecretResolver

	initJobRunner initJobExecutor
	serviceWaiter serviceWaitExecutor
}

type InitJobMetrics interface {
	// RecordInitJobRun records one init job run by stack and service.
	RecordInitJobRun(stack, service string)
}

type initJobExecutor interface {
	// Run executes one init job based on deployment context and job spec.
	Run(ctx context.Context, spec InitJobSpec) (InitJobResult, error)
}

type serviceWaitExecutor interface {
	// Snapshot returns the current services belonging to a stack.
	Snapshot(ctx context.Context, stackName string) ([]swarm.StackService, error)
	// Wait blocks until services affected by the apply converge.
	Wait(ctx context.Context, request ServiceWaitRequest) error
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
		stackDeployArgs: defaultStackDeployArgs(),
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
		serviceWaiter: NewServiceWaiter(
			swarmService.Services,
			initJobPoll,
			defaultServiceWaitTimeout,
		),
	}

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return deployer
	}

	return newTraceableDeployer(tp, deployer)
}

func defaultStackDeployArgs() []string {
	return []string{"stack", "deploy", "--with-registry-auth", "--detach=true", "--quiet"}
}

const binaryTimeout = 2 * time.Minute

func (d *Deployer) DeployStack(
	ctx context.Context,
	stackName,
	sourceComposePath,
	deployComposePath string,
	desired compose.Compose,
) (Result, error) {
	result := Result{}
	resolved, err := d.reconcileResources(ctx, stackName, sourceComposePath, desired)
	if err != nil {
		return result, err
	}

	result.InitJobs, err = d.runInitJobs(ctx, stackName, desired.Services, resolved)
	if err != nil {
		return result, err
	}

	serviceNames := make([]string, 0, len(desired.Services))
	for _, service := range desired.Services {
		serviceNames = append(serviceNames, service.Name)
	}

	return result, d.applyCompose(ctx, stackName, deployComposePath, serviceNames, true)
}

func (d *Deployer) reconcileResources(
	ctx context.Context, stackName, sourceComposePath string, desired compose.Compose,
) (resolvedResources, error) {
	resolved := resolvedResources{}
	if len(desired.Configs) == 0 && len(desired.Secrets) == 0 {
		return resolved, nil
	}

	data, err := d.secretResolver.Resolve(ctx, stackName, sourceComposePath, desired.Secrets)
	if err != nil {
		return resolved, fmt.Errorf("resolve secrets: %w", err)
	}
	resolved, err = d.resources.Reconcile(ctx, stackName, desired.Configs, desired.Secrets, data)
	clearSecretData(data)
	if err != nil {
		return resolved, fmt.Errorf("reconcile configs and secrets: %w", err)
	}

	return resolved, nil
}

func (d *Deployer) applyCompose(
	ctx context.Context,
	stackName,
	deployComposePath string,
	serviceNames []string,
	onlyAffected bool,
) error {
	baseline, err := d.serviceWaiter.Snapshot(ctx, stackName)
	if err != nil {
		return err
	}

	args := make([]string, 0, len(d.stackDeployArgs)+deployArgsExtraCount)
	args = append(args, d.stackDeployArgs...)
	args = append(args, "-c", deployComposePath, stackName)

	commandCtx, cancel := context.WithTimeout(ctx, binaryTimeout)
	defer cancel()

	if _, err = d.runner.Run(commandCtx, args...); err != nil {
		return fmt.Errorf("deploy stack %s: %w", stackName, err)
	}

	if err = d.serviceWaiter.Wait(ctx, ServiceWaitRequest{
		StackName: stackName, ServiceNames: serviceNames, Baseline: baseline, OnlyAffected: onlyAffected,
	}); err != nil {
		return fmt.Errorf("wait for stack %s service convergence: %w", stackName, err)
	}

	return nil
}

// DeployService applies one service from the desired stack definition.
func (d *Deployer) DeployService(
	ctx context.Context,
	stackName,
	sourceComposePath,
	serviceName string,
	desired,
	deploy compose.Compose,
) (Result, error) {
	serviceDesired, err := singleServiceCompose(desired, serviceName)
	if err != nil {
		return Result{}, err
	}
	serviceDeploy, err := singleServiceCompose(deploy, serviceName)
	if err != nil {
		return Result{}, err
	}
	if _, err = d.reconcileResources(ctx, stackName, sourceComposePath, serviceDesired); err != nil {
		return Result{}, err
	}

	payload, err := yaml.Marshal(serviceDeploy)
	if err != nil {
		return Result{}, fmt.Errorf("marshal single service compose: %w", err)
	}

	file, err := os.CreateTemp("", "swarm-deploy-single-service-*.yaml")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary compose file: %w", err)
	}
	targetPath := file.Name()
	defer func() { _ = os.Remove(targetPath) }()

	if _, err = file.Write(payload); err != nil {
		_ = file.Close()
		return Result{}, fmt.Errorf("write temporary compose file: %w", err)
	}
	if err = file.Close(); err != nil {
		return Result{}, fmt.Errorf("close temporary compose file: %w", err)
	}

	return Result{}, d.applyCompose(ctx, stackName, targetPath, []string{serviceName}, false)
}

func singleServiceCompose(desired compose.Compose, serviceName string) (compose.Compose, error) {
	for _, service := range desired.Services {
		if service.Name != serviceName {
			continue
		}

		desired.Services = compose.Services{service}
		desired.Networks = selectNetworks(desired.Networks, service.Networks)
		desired.Secrets = selectObjects(desired.Secrets, service.Secrets)
		desired.Configs = selectObjects(desired.Configs, service.Configs)
		desired.Volumes = selectVolumes(desired.Volumes, service.Volumes)
		return desired, nil
	}

	return compose.Compose{}, fmt.Errorf("service %q not found in compose", serviceName)
}

func selectNetworks(networks map[string]compose.Network, refs *compose.ServiceNetworks) map[string]compose.Network {
	if len(networks) == 0 {
		return nil
	}
	if refs == nil {
		defaultNetwork, ok := networks["default"]
		if !ok {
			return nil
		}
		return map[string]compose.Network{"default": defaultNetwork}
	}
	selected := make(map[string]compose.Network)
	for _, alias := range refs.GetAliases() {
		if network, ok := networks[alias]; ok {
			selected[alias] = network
		}
	}
	return selected
}

func selectObjects[T any, Objects ~map[string]*T](objects Objects, refs []compose.ObjectRef) Objects {
	if len(objects) == 0 || len(refs) == 0 {
		return nil
	}
	selected := make(Objects)
	for _, ref := range refs {
		if object, ok := objects[ref.Source]; ok {
			selected[ref.Source] = object
		}
	}
	return selected
}

func selectVolumes(volumes compose.Volumes, refs compose.ServiceVolumes) compose.Volumes {
	if len(volumes) == 0 || len(refs.Volumes) == 0 {
		return nil
	}
	selected := make(compose.Volumes)
	for _, ref := range refs.Volumes {
		if ref.Type != compose.ServiceVolumeTypeVolume {
			continue
		}
		if volume, ok := volumes[ref.Source]; ok {
			selected[ref.Source] = volume
		}
	}
	return selected
}

func (d *Deployer) runInitJobs(
	ctx context.Context,
	stackName string,
	services []compose.Service,
	resolved resolvedResources,
) ([]InitJobResult, error) {
	result := make([]InitJobResult, 0)
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
				result = append(result, execution)
			}
			if err != nil {
				return result, fmt.Errorf("service %s init job %s: %w", service.Name, job.Name, err)
			}
		}
	}
	return result, nil
}
