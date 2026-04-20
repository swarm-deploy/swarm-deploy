package deployer

import (
	"context"
	"fmt"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

const secretOrConfigFileMode = 0o444

func (r *InitJobRunner) buildInitServiceSpec(
	ctx context.Context,
	spec InitJobSpec,
	serviceName string,
) (dockerswarm.ServiceSpec, error) {
	containerSpec := &dockerswarm.ContainerSpec{
		Image:   spec.Job.Image,
		Command: spec.Job.Command,
	}

	if len(spec.Job.Environment) > 0 {
		containerSpec.Env = make([]string, 0, len(spec.Job.Environment))
		for key, val := range spec.Job.Environment {
			containerSpec.Env = append(containerSpec.Env, fmt.Sprintf("%s=%s", key, val))
		}
	}

	networks := spec.Job.Networks
	if len(networks) == 0 {
		networks = spec.DefaultNetwork
	}
	networks = uniqueStrings(networks)

	networkAttachments := make([]dockerswarm.NetworkAttachmentConfig, 0, len(networks))
	for _, network := range networks {
		target := r.resolveNetworkTarget(ctx, spec.StackName, network)
		if target == "" {
			continue
		}
		networkAttachments = append(networkAttachments, dockerswarm.NetworkAttachmentConfig{Target: target})
	}

	secrets := mergeObjectRefs(spec.ServiceSecrets, spec.Job.Secrets)
	containerSpec.Secrets = make([]*dockerswarm.SecretReference, 0, len(secrets))
	for _, secret := range secrets {
		ref, err := r.secretResolver.ResolveReference(ctx, secret.Source, secret.Target)
		if err != nil {
			return dockerswarm.ServiceSpec{}, err
		}
		containerSpec.Secrets = append(containerSpec.Secrets, ref)
	}

	configs := mergeObjectRefs(spec.ServiceConfigs, spec.Job.Configs)
	containerSpec.Configs = make([]*dockerswarm.ConfigReference, 0, len(configs))
	for _, cfg := range configs {
		ref, ok, err := r.resolveConfigReference(ctx, spec.StackName, cfg.Source, cfg.Target)
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
			Name: serviceName,
			Labels: map[string]string{
				"org.swarm-deploy.init-job.name":    serviceName,
				"org.swarm-deploy.init-job.stack":   spec.StackName,
				"org.swarm-deploy.init-job.service": spec.ServiceName,
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

func (r *InitJobRunner) resolveNetworkTarget(ctx context.Context, stackName, network string) string {
	candidates := []string{network}
	if !strings.HasPrefix(network, stackName+"_") {
		candidates = append(candidates, stackName+"_"+network)
	}
	if network == "default" {
		candidates = append(candidates, stackName+"_default")
	}

	for _, candidate := range uniqueStrings(candidates) {
		netResource, err := r.dockerClient.NetworkInspect(ctx, candidate, dockernetwork.InspectOptions{})
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

func (r *InitJobRunner) resolveConfigReference(
	ctx context.Context,
	stackName, source, target string,
) (*dockerswarm.ConfigReference, bool, error) {
	candidates := []string{source}
	if !strings.HasPrefix(source, stackName+"_") {
		candidates = append(candidates, stackName+"_"+source)
	}

	for _, candidate := range uniqueStrings(candidates) {
		cfg, _, err := r.dockerClient.ConfigInspectWithRaw(ctx, candidate)
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
