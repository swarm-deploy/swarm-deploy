package swarm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/filters"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

const secretOrConfigFileMode = 0o444

func (d *Deployer) runInitJobAPI(ctx context.Context, spec InitJobSpec) error {
	if d.dockerClient == nil {
		return errors.New("docker api client is not initialized")
	}
	if spec.Job.Image == "" {
		return errors.New("init job image is required")
	}

	timeout := d.initJobTimeout
	if spec.Job.Timeout > 0 {
		timeout = spec.Job.Timeout
	}
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	jobName := buildInitJobName(spec.StackName, spec.ServiceName, spec.Job.Name)

	serviceSpec, err := d.buildInitServiceSpecAPI(jobCtx, spec, jobName)
	if err != nil {
		return err
	}

	serviceCreateOptions, err := d.buildInitServiceCreateOptions(spec.Job.Image)
	if err != nil {
		return fmt.Errorf("build init job service create options: %w", err)
	}

	serviceCreate, err := d.dockerClient.ServiceCreate(jobCtx, serviceSpec, serviceCreateOptions)
	if err != nil {
		return fmt.Errorf("create init job service %s: %w", jobName, err)
	}

	serviceID := serviceCreate.ID
	defer func() {
		_ = d.dockerClient.ServiceRemove(context.Background(), serviceID)
	}()

	err = d.waitForJobCompletionAPI(jobCtx, serviceID, jobName)
	if err != nil {
		return err
	}

	return nil
}

func (d *Deployer) buildInitServiceSpecAPI(
	ctx context.Context,
	spec InitJobSpec,
	jobName string,
) (dockerswarm.ServiceSpec, error) {
	containerSpec := &dockerswarm.ContainerSpec{
		Image:   spec.Job.Image,
		Command: spec.Job.Command,
	}

	if len(spec.Job.Environment) > 0 {
		keys := make([]string, 0, len(spec.Job.Environment))
		for key := range spec.Job.Environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		containerSpec.Env = make([]string, 0, len(keys))
		for _, key := range keys {
			containerSpec.Env = append(containerSpec.Env, fmt.Sprintf("%s=%s", key, spec.Job.Environment[key]))
		}
	}

	networks := spec.Job.Networks
	if len(networks) == 0 {
		networks = spec.DefaultNetwork
	}
	networks = uniqueStrings(networks)

	networkAttachments := make([]dockerswarm.NetworkAttachmentConfig, 0, len(networks))
	for _, network := range networks {
		target := d.resolveNetworkTargetAPI(ctx, spec.StackName, network)
		if target == "" {
			continue
		}
		networkAttachments = append(networkAttachments, dockerswarm.NetworkAttachmentConfig{Target: target})
	}

	secrets := mergeObjectRefs(spec.ServiceSecrets, spec.Job.Secrets)
	containerSpec.Secrets = make([]*dockerswarm.SecretReference, 0, len(secrets))
	for _, secret := range secrets {
		ref, ok, err := d.resolveSecretReferenceAPI(ctx, spec.StackName, secret.Source, secret.Target)
		if err != nil {
			return dockerswarm.ServiceSpec{}, err
		}
		if !ok {
			continue
		}
		containerSpec.Secrets = append(containerSpec.Secrets, ref)
	}

	configs := mergeObjectRefs(spec.ServiceConfigs, spec.Job.Configs)
	containerSpec.Configs = make([]*dockerswarm.ConfigReference, 0, len(configs))
	for _, cfg := range configs {
		ref, ok, err := d.resolveConfigReferenceAPI(ctx, spec.StackName, cfg.Source, cfg.Target)
		if err != nil {
			return dockerswarm.ServiceSpec{}, err
		}
		if !ok {
			continue
		}
		containerSpec.Configs = append(containerSpec.Configs, ref)
	}

	replicas := uint64(1)

	return dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{
			Name: jobName,
			Labels: map[string]string{
				"swarmdeploy.io/init-job": "true",
				"swarmdeploy.io/stack":    spec.StackName,
				"swarmdeploy.io/service":  spec.ServiceName,
			},
		},
		TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: containerSpec,
			Networks:      networkAttachments,
			RestartPolicy: &dockerswarm.RestartPolicy{
				Condition: dockerswarm.RestartPolicyConditionNone,
			},
		},
		Mode: dockerswarm.ServiceMode{
			Replicated: &dockerswarm.ReplicatedService{
				Replicas: &replicas,
			},
		},
	}, nil
}

func (d *Deployer) waitForJobCompletionAPI(ctx context.Context, serviceID, jobName string) error {
	ticker := time.NewTicker(d.initJobPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait init job %s: %w", jobName, ctx.Err())
		case <-ticker.C:
			tasks, err := d.dockerClient.TaskList(ctx, dockerswarm.TaskListOptions{
				Filters: filters.NewArgs(filters.Arg("service", serviceID)),
			})
			if err != nil {
				return fmt.Errorf("inspect init job %s status: %w", jobName, err)
			}
			if len(tasks) == 0 {
				continue
			}

			sort.Slice(tasks, func(i, j int) bool {
				return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
			})

			task := tasks[0]
			state := task.Status.State
			switch state {
			case dockerswarm.TaskStateNew,
				dockerswarm.TaskStateAllocated,
				dockerswarm.TaskStatePending,
				dockerswarm.TaskStateAssigned,
				dockerswarm.TaskStateAccepted,
				dockerswarm.TaskStatePreparing,
				dockerswarm.TaskStateReady,
				dockerswarm.TaskStateStarting,
				dockerswarm.TaskStateRunning:
				continue
			case dockerswarm.TaskStateComplete:
				return nil
			case dockerswarm.TaskStateFailed,
				dockerswarm.TaskStateRejected,
				dockerswarm.TaskStateShutdown,
				dockerswarm.TaskStateOrphaned,
				dockerswarm.TaskStateRemove:
				reason := strings.TrimSpace(task.Status.Err)
				if reason == "" {
					reason = strings.TrimSpace(task.Status.Message)
				}
				if reason == "" {
					reason = string(state)
				}
				return fmt.Errorf("init job %s failed: %s", jobName, reason)
			}
		}
	}
}

func (d *Deployer) resolveNetworkTargetAPI(ctx context.Context, stackName, network string) string {
	candidates := []string{network}
	if !strings.HasPrefix(network, stackName+"_") {
		candidates = append(candidates, stackName+"_"+network)
	}
	if network == "default" {
		candidates = append(candidates, stackName+"_default")
	}

	for _, candidate := range uniqueStrings(candidates) {
		netResource, err := d.dockerClient.NetworkInspect(ctx, candidate, dockernetwork.InspectOptions{})
		if err == nil {
			return netResource.ID
		}
		if !cerrdefs.IsNotFound(err) {
			// Fall through to try other candidates.
			continue
		}
	}

	return network
}

func (d *Deployer) resolveSecretReferenceAPI(
	ctx context.Context,
	stackName, source, target string,
) (*dockerswarm.SecretReference, bool, error) {
	candidates := []string{source}
	if !strings.HasPrefix(source, stackName+"_") {
		candidates = append(candidates, stackName+"_"+source)
	}

	for _, candidate := range uniqueStrings(candidates) {
		secret, _, err := d.dockerClient.SecretInspectWithRaw(ctx, candidate)
		if err == nil {
			ref := &dockerswarm.SecretReference{
				SecretID:   secret.ID,
				SecretName: secret.Spec.Name,
			}
			if target == "" {
				target = fmt.Sprintf("/run/secrets/%s", ref.SecretName)
			}

			ref.File = &dockerswarm.SecretReferenceFileTarget{
				Name: target,
				UID:  "0",
				GID:  "0",
				Mode: secretOrConfigFileMode,
			}
			return ref, true, nil
		}
		if !cerrdefs.IsNotFound(err) {
			return nil, false, fmt.Errorf("inspect secret %s: %w", candidate, err)
		}
	}

	ref := &dockerswarm.SecretReference{
		SecretName: source,
	}
	if target != "" {
		ref.File = &dockerswarm.SecretReferenceFileTarget{
			Name: target,
			UID:  "0",
			GID:  "0",
			Mode: secretOrConfigFileMode,
		}
	}
	return ref, true, nil
}

func (d *Deployer) resolveConfigReferenceAPI(
	ctx context.Context,
	stackName, source, target string,
) (*dockerswarm.ConfigReference, bool, error) {
	candidates := []string{source}
	if !strings.HasPrefix(source, stackName+"_") {
		candidates = append(candidates, stackName+"_"+source)
	}

	for _, candidate := range uniqueStrings(candidates) {
		cfg, _, err := d.dockerClient.ConfigInspectWithRaw(ctx, candidate)
		if err == nil {
			ref := &dockerswarm.ConfigReference{
				ConfigID:   cfg.ID,
				ConfigName: cfg.Spec.Name,
			}
			if target != "" {
				ref.File = &dockerswarm.ConfigReferenceFileTarget{
					Name: target,
					UID:  "0",
					GID:  "0",
					Mode: secretOrConfigFileMode,
				}
			}
			return ref, true, nil
		}
		if !cerrdefs.IsNotFound(err) {
			return nil, false, fmt.Errorf("inspect config %s: %w", candidate, err)
		}
	}

	ref := &dockerswarm.ConfigReference{
		ConfigName: source,
	}
	if target != "" {
		ref.File = &dockerswarm.ConfigReferenceFileTarget{
			Name: target,
			UID:  "0",
			GID:  "0",
			Mode: secretOrConfigFileMode,
		}
	}
	return ref, true, nil
}
