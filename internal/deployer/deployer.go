package deployer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
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

	return d.runStackDeploy(ctx, stackName, deployComposePath)
}

func (d *Deployer) DeployService(
	ctx context.Context,
	stackName,
	composePath string,
	service compose.Service,
) error {
	source, err := compose.NewFileLoader().Load(ctx, composePath)
	if err != nil {
		return fmt.Errorf("load compose for service %s/%s: %w", stackName, service.Name, err)
	}

	found := false
	for _, sourceService := range source.Compose.Services {
		if sourceService.Name == service.Name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("service %q not found in compose", service.Name)
	}

	desired := source.Compose
	desired.Services = compose.Services{service}

	resolved := resolvedResources{}
	if hasInitJobs(desired.Services) && (len(desired.Configs) > 0 || len(desired.Secrets) > 0) && d.resources != nil {
		resolved, err = d.resources.Reconcile(ctx, stackName, composePath, desired.Configs, desired.Secrets)
		if err != nil {
			return fmt.Errorf("reconcile init job configs and secrets: %w", err)
		}
	}

	if err := d.runInitJobs(ctx, stackName, desired.Services, resolved); err != nil {
		return err
	}

	content, err := (&compose.File{Compose: desired}).MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal compose for service %s/%s: %w", stackName, service.Name, err)
	}

	file, err := os.CreateTemp(filepath.Dir(composePath), "swarm-deploy-single-service-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary compose file: %w", err)
	}
	targetPath := file.Name()
	defer os.Remove(targetPath)

	if _, err := file.Write(content); err != nil {
		file.Close()
		return fmt.Errorf("write temporary compose file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary compose file: %w", err)
	}

	return d.runStackDeploy(ctx, stackName, targetPath)
}

func (d *Deployer) runStackDeploy(ctx context.Context, stackName, deployComposePath string) error {
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
) error {
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
