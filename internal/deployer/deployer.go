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
	"gopkg.in/yaml.v3"
)

const deployArgsExtraCount = 3

type Deployer struct {
	stackDeployArgs []string
	runner          Runner
	resources       *resourceReconciler

	initJobRunner initJobExecutor
}

type InitJobMetrics interface {
	// RecordInitJobRun records one init job run by stack and service.
	RecordInitJobRun(stack, service string)
}

type initJobExecutor interface {
	// Run executes one init job based on deployment context and job spec.
	Run(ctx context.Context, spec InitJobSpec) error
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
) StackDeployer {
	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy", "--with-registry-auth", "--detach=false", "--quiet"},
		runner:          swarmService.BinaryRunner,
		resources:       newResourceReconciler(dockerClient),
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

const binaryTimeout = 1 * time.Minute

func (d *Deployer) DeployStack(
	ctx context.Context,
	stackName,
	sourceComposePath,
	deployComposePath string,
	desired compose.Compose,
) error {
	resolved := resolvedResources{}
	if hasInitJobs(desired.Services) && (len(desired.Configs) > 0 || len(desired.Secrets) > 0) {
		var err error
		resolved, err = d.resources.Reconcile(ctx, stackName, sourceComposePath, desired.Configs, desired.Secrets)
		if err != nil {
			return fmt.Errorf("reconcile init job configs and secrets: %w", err)
		}
	}

	if err := d.runInitJobs(ctx, stackName, desired.Services, resolved); err != nil {
		return err
	}

	return d.runStackDeploy(ctx, stackName, composePath)
}

func (d *Deployer) DeployService(ctx context.Context, stackName, composePath string, service compose.Service) error {
	if err := d.runInitJobs(ctx, stackName, []compose.Service{service}); err != nil {
		return err
	}

	serviceComposePath, cleanup, err := createSingleServiceCompose(composePath, service.Name)
	if err != nil {
		return fmt.Errorf("render single service compose for %s/%s: %w", stackName, service.Name, err)
	}
	defer cleanup()

	return d.runStackDeploy(ctx, stackName, serviceComposePath)
}

func (d *Deployer) runStackDeploy(ctx context.Context, stackName, composePath string) error {
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

<<<<<<< HEAD
func createSingleServiceCompose(composePath string, serviceName string) (string, func(), error) {
	stackFile, err := compose.Load(composePath)
	if err != nil {
		return "", nil, err
	}

	servicesMap, isMap := asMap(stackFile.RawMap["services"])
	if !isMap {
		return "", nil, fmt.Errorf("services section is not a map")
	}

	serviceRaw, hasService := servicesMap[serviceName]
	if !hasService {
		return "", nil, fmt.Errorf("service %q not found in compose", serviceName)
	}

	rendered := map[string]any{
		"services": map[string]any{
			serviceName: serviceRaw,
		},
	}

	for _, key := range []string{"version", "name", "networks", "secrets", "volumes", "configs"} {
		if value, ok := stackFile.RawMap[key]; ok {
			rendered[key] = value
		}
	}

	payload, err := yaml.Marshal(rendered)
	if err != nil {
		return "", nil, fmt.Errorf("marshal single service compose: %w", err)
	}

	file, err := os.CreateTemp("", "swarm-deploy-single-service-*.yaml")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary compose file: %w", err)
	}

	targetPath := file.Name()
	if _, err = file.Write(payload); err != nil {
		file.Close()
		_ = os.Remove(targetPath)
		return "", nil, fmt.Errorf("write temporary compose file: %w", err)
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(targetPath)
		return "", nil, fmt.Errorf("close temporary compose file: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(targetPath)
	}

	return targetPath, cleanup, nil
}

func asMap(v any) (map[string]any, bool) {
	if typed, ok := v.(map[string]any); ok {
		return typed, true
	}

	typedAny, ok := v.(map[any]any)
	if !ok {
		return nil, false
	}

	out := make(map[string]any, len(typedAny))
	for key, value := range typedAny {
		asString, keyIsString := key.(string)
		if !keyIsString {
			continue
		}
		out[asString] = value
	}

	return out, true
}

func (d *Deployer) runInitJobs(ctx context.Context, stackName string, services []compose.Service) error {
=======
func (d *Deployer) runInitJobs(
	ctx context.Context,
	stackName string,
	services []compose.Service,
	resolved resolvedResources,
) error {
>>>>>>> origin/master
	for _, service := range services {
		// Jobs are run in declaration order per service to keep behavior deterministic.
		for _, job := range service.InitJobs {
			err := d.initJobRunner.Run(ctx, InitJobSpec{
				StackName:       stackName,
				ServiceName:     service.Name,
				DefaultNetwork:  service.Networks.GetNames(),
				ServiceSecrets:  service.Secrets,
				ServiceConfigs:  service.Configs,
				ResolvedSecrets: resolved.secrets,
				ResolvedConfigs: resolved.configs,
				Job:             job,
			})
			if err != nil {
				return fmt.Errorf("service %s init job %s: %w", service.Name, job.Name, err)
			}
		}
	}
	return nil
}

func hasInitJobs(services []compose.Service) bool {
	for _, service := range services {
		if len(service.InitJobs) > 0 {
			return true
		}
	}

	return false
}
