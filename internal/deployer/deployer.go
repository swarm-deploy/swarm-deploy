package deployer

import (
	"context"
	"fmt"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/docker/docker/client"
)

const deployArgsExtraCount = 3

type Deployer struct {
	stackDeployArgs []string
	runner          Runner

	initJobRunner *InitJobRunner
}

type InitJobMetrics interface {
	// RecordInitJobRun records one init job run by stack and service.
	RecordInitJobRun(stack, service string)
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
			swarmService.Services,
			swarmService.Secrets,
			initJobPoll,
			initJobTimeout,
			initJobMetrics,
		),
	}
}

func (d *Deployer) DeployStack(ctx context.Context, stackName, composePath string) error {
	args := make([]string, 0, len(d.stackDeployArgs)+deployArgsExtraCount)
	args = append(args, d.stackDeployArgs...)
	args = append(args, "-c", composePath, stackName)

	if _, err := d.runner.Run(ctx, args...); err != nil {
		return fmt.Errorf("deploy stack %s: %w", stackName, err)
	}
	return nil
}

func (d *Deployer) RunInitJob(ctx context.Context, spec InitJobSpec) error {
	return d.initJobRunner.Run(ctx, spec)
}
