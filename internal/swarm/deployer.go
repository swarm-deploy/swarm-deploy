package swarm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/swarm/initjob"
	"github.com/artarts36/swarm-deploy/internal/swarm/secret"
	"github.com/docker/docker/client"
)

const deployArgsExtraCount = 3

type Deployer struct {
	command         string
	stackDeployArgs []string
	initJobPoll     time.Duration
	initJobTimeout  time.Duration
	runner          Runner
	dockerClient    *client.Client
	authManager     registry.AuthManager
	secretResolver  *secret.Resolver

	initJobRunner *initjob.Runner
}

type InitJobSpec struct {
	StackName      string
	ServiceName    string
	DefaultNetwork []string
	ServiceSecrets []compose.ObjectRef
	ServiceConfigs []compose.ObjectRef
	Job            compose.InitJob
}

func NewDeployer(
	command string,
	stackDeployArgs []string,
	initJobPoll time.Duration,
	initJobTimeout time.Duration,
	runner Runner,
) (*Deployer, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker api client: %w", err)
	}

	return &Deployer{
		command:         command,
		stackDeployArgs: stackDeployArgs,
		initJobPoll:     initJobPoll,
		initJobTimeout:  initJobTimeout,
		runner:          runner,
		dockerClient:    cli,
		authManager:     registry.NewAuthManager(),
		secretResolver:  secret.NewResolver(cli),
		initJobRunner:   initjob.NewRunner(cli, initJobPoll),
	}, nil
}

func (d *Deployer) DeployStack(ctx context.Context, stackName, composePath string) error {
	args := make([]string, 0, len(d.stackDeployArgs)+deployArgsExtraCount)
	args = append(args, d.stackDeployArgs...)
	args = append(args, "-c", composePath, stackName)

	if _, err := d.runner.Run(ctx, d.command, args...); err != nil {
		return fmt.Errorf("deploy stack %s: %w", stackName, err)
	}
	return nil
}

func (d *Deployer) RunInitJob(ctx context.Context, spec InitJobSpec) error {
	return d.runInitJobAPI(ctx, spec)
}

func buildInitJobName(_, _, jobName string) string {
	return fmt.Sprintf("%s-%d", sanitizeForName(jobName), time.Now().UnixNano())
}

func sanitizeForName(v string) string {
	if v == "" {
		return "job"
	}
	var out strings.Builder
	for _, r := range strings.ToLower(v) {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}
	result := strings.Trim(out.String(), "-")
	if result == "" {
		return "job"
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func mergeObjectRefs(a, b []compose.ObjectRef) []compose.ObjectRef {
	seen := map[string]struct{}{}
	out := make([]compose.ObjectRef, 0, len(a)+len(b))

	appendOne := func(ref compose.ObjectRef) {
		key := ref.Source + "|" + ref.Target
		if ref.Source == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}

	for _, ref := range a {
		appendOne(ref)
	}
	for _, ref := range b {
		appendOne(ref)
	}
	return out
}
