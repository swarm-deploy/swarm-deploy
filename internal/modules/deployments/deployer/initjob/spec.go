package initjob

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
)

func (r *InitJobRunner) buildInitServiceSpec(
	ctx context.Context,
	spec Spec,
	serviceName string,
) (dockerswarm.ServiceSpec, error) {
	containerSpec := &dockerswarm.ContainerSpec{
		Image: spec.Job.Image, Command: spec.Job.Entrypoint, Args: spec.Job.Command,
	}
	containerSpec.Env = initJobEnvironment(spec.Job.Environment.Map)
	networkAttachments := r.initJobNetworks(ctx, spec)
	if err := r.addInitJobObjects(ctx, spec, containerSpec); err != nil {
		return dockerswarm.ServiceSpec{}, err
	}

	replicas := uint64(1)
	return dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{Name: serviceName, Labels: map[string]string{
			"org.swarm-deploy.init-job.name": serviceName, "org.swarm-deploy.init-job.stack": spec.StackName,
			"org.swarm-deploy.init-job.service": spec.ServiceName,
		}},
		TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: containerSpec, Networks: networkAttachments,
			RestartPolicy: &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}},
		Mode: dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &replicas}},
	}, nil
}

func initJobEnvironment(environment map[string]string) []string {
	if len(environment) == 0 {
		return nil
	}
	env := make([]string, 0, len(environment))
	for key, value := range environment {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func (r *InitJobRunner) initJobNetworks(ctx context.Context, spec Spec) []dockerswarm.NetworkAttachmentConfig {
	networks := networkAliases(spec.Job.Networks)
	if len(networks) == 0 {
		networks = spec.DefaultNetwork
	}

	attachments := make([]dockerswarm.NetworkAttachmentConfig, 0, len(networks))
	for _, network := range uniqueStrings(networks) {
		target := spec.NetworkTargets[network]
		if target == "" {
			target = r.resolveNetworkTarget(ctx, spec.StackName, network)
		}
		if target != "" {
			attachments = append(attachments, dockerswarm.NetworkAttachmentConfig{Target: target})
		}
	}
	return attachments
}

func (r *InitJobRunner) addInitJobObjects(
	ctx context.Context,
	spec Spec,
	containerSpec *dockerswarm.ContainerSpec,
) error {
	for _, secret := range mergeObjectRefs(spec.ServiceSecrets, spec.Job.Secrets) {
		ref := resolvedSecretReference(secret, spec.ResolvedSecrets)
		if ref == nil {
			var err error
			ref, err = r.swarmService.Secrets.ResolveReference(ctx, secret.Source, secret.Target)
			if err != nil {
				return err
			}
		}
		containerSpec.Secrets = append(containerSpec.Secrets, ref)
	}
	for _, cfg := range mergeObjectRefs(spec.ServiceConfigs, spec.Job.Configs) {
		ref := resolvedConfigReference(cfg, spec.ResolvedConfigs)
		if ref == nil {
			var err error
			ref, err = r.swarmService.Configs.ResolveReference(ctx, cfg.Source, cfg.Target)
			if err != nil {
				return err
			}
		}
		containerSpec.Configs = append(containerSpec.Configs, ref)
	}
	return nil
}

func networkAliases(networks *compose.ServiceNetworks) []string {
	if networks == nil {
		return nil
	}
	if aliases := networks.GetAliases(); len(aliases) > 0 {
		return aliases
	}
	return networks.GetNames()
}

func resolvedSecretReference(
	object compose.ObjectRef,
	resolved map[string]resources.ResolvedResource,
) *dockerswarm.SecretReference {
	resource, ok := resolved[object.Source]
	if !ok {
		return nil
	}
	target := object.Target
	if target == "" {
		target = "/run/secrets/" + object.Source
	}
	uid, gid := objectOwner(object)
	return &dockerswarm.SecretReference{SecretID: resource.ID, SecretName: resource.Name,
		File: &dockerswarm.SecretReferenceFileTarget{Name: target, UID: uid, GID: gid, Mode: objectMode(object.Mode)}}
}

func resolvedConfigReference(
	object compose.ObjectRef,
	resolved map[string]resources.ResolvedResource,
) *dockerswarm.ConfigReference {
	resource, ok := resolved[object.Source]
	if !ok {
		return nil
	}
	ref := &dockerswarm.ConfigReference{ConfigID: resource.ID, ConfigName: resource.Name}
	if object.Target != "" {
		uid, gid := objectOwner(object)
		ref.File = &dockerswarm.ConfigReferenceFileTarget{
			Name: object.Target, UID: uid, GID: gid, Mode: objectMode(object.Mode),
		}
	}
	return ref
}

func objectMode(mode *os.FileMode) os.FileMode {
	if mode == nil {
		return initJobObjectFileMode
	}
	return *mode
}

func objectOwner(object compose.ObjectRef) (string, string) {
	uid, gid := object.Uid, object.Gid
	if uid == "" {
		uid = "0"
	}
	if gid == "" {
		gid = "0"
	}
	return uid, gid
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
		netResource, err := r.swarmService.Networks.Get(ctx, candidate)
		if err == nil {
			return netResource.ID
		}
		if !cerrdefs.IsNotFound(err) {
			continue
		}
	}
	return network
}

// BuildName returns the ephemeral Docker service name for one Init Job execution.
func BuildName(_, _, jobName string) string {
	return fmt.Sprintf("%s-%d", sanitizeForName(jobName), time.Now().UnixNano())
}

func sanitizeForName(v string) string {
	if v == "" {
		return "job"
	}
	var out strings.Builder
	for _, r := range strings.ToLower(v) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
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
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
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
