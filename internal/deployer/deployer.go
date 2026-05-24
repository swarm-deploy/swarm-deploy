package deployer

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const deployArgsExtraCount = 3

type Deployer struct {
	stackDeployArgs []string
	runner          Runner

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
	// Job is a source compose init job specification.
	Job compose.InitJob
}

func NewDeployer(
	stackDeployArgs []string,
	initJobPoll time.Duration,
	initJobTimeout time.Duration,
	runner Runner,
	dockerClient *client.Client,
	swarmService *swarm.Swarm,
	initJobMetrics InitJobMetrics,
) *Deployer {
	return &Deployer{
		stackDeployArgs: stackDeployArgs,
		runner:          runner,
		initJobRunner: NewInitJobRunner(
			dockerClient,
			swarmService,
			initJobPoll,
			initJobTimeout,
			initJobMetrics,
		),
	}
}

func (d *Deployer) DeployStack(ctx context.Context, stackName, composePath string, services []compose.Service) error {
	if err := d.runInitJobs(ctx, stackName, services); err != nil {
		return err
	}

	args := make([]string, 0, len(d.stackDeployArgs)+deployArgsExtraCount)
	args = append(args, d.stackDeployArgs...)
	args = append(args, "-c", composePath, stackName)

	if _, err := d.runner.Run(ctx, args...); err != nil {
		return fmt.Errorf("deploy stack %s: %w", stackName, err)
	}
	return nil
}

func (d *Deployer) runInitJobs(ctx context.Context, stackName string, services []compose.Service) error {
	for _, service := range services {
		serviceNetworkNames := make([]string, len(service.Networks))
		for i, network := range service.Networks {
			serviceNetworkNames[i] = network.Name
		}

		// Jobs are run in declaration order per service to keep behavior deterministic.
		for _, job := range service.InitJobs {
			err := d.initJobRunner.Run(ctx, InitJobSpec{
				StackName:      stackName,
				ServiceName:    service.Name,
				DefaultNetwork: serviceNetworkNames,
				ServiceSecrets: service.Secrets,
				ServiceConfigs: service.Configs,
				Job:            job,
			})
			if err != nil {
				return fmt.Errorf("service %s init job %s: %w", service.Name, job.Name, err)
			}
		}
	}
	return nil
}
