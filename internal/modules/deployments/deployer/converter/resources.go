package converter

import (
	"context"
	"fmt"
	"os"
	"slices"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (c *Converter) resolveObjects(
	ctx context.Context, stackName string, desired compose.Compose,
) (resources.Resolved, error) {
	result := resources.Resolved{
		Configs: map[string]resources.ResolvedResource{}, Secrets: map[string]resources.ResolvedResource{},
	}
	configNames := make([]string, 0, len(desired.Configs))
	for _, alias := range sortedKeys(desired.Configs) {
		object := desired.Configs[alias]
		configNames = append(configNames, resourceName(stackName, alias, object.Name, object.External))
	}
	if len(configNames) > 0 {
		existing, err := c.configs.List(ctx, swarm.ListConfigsFilter{Names: configNames})
		if err != nil {
			return result, faults.SanitizeError(err, "configs: Docker metadata lookup failed")
		}
		for _, alias := range sortedKeys(desired.Configs) {
			object := desired.Configs[alias]
			name := resourceName(stackName, alias, object.Name, object.External)
			index := slices.IndexFunc(existing, func(r swarm.Config) bool { return r.Name == name && r.ID != "" })
			if index < 0 {
				return result, fmt.Errorf("configs.%s: required config unavailable; reconcile it before conversion", alias)
			}
			result.Configs[alias] = resources.ResolvedResource{ID: existing[index].ID, Name: existing[index].Name}
		}
	}
	secretNames := make([]string, 0, len(desired.Secrets))
	for _, alias := range sortedKeys(desired.Secrets) {
		object := desired.Secrets[alias]
		secretNames = append(secretNames, resourceName(stackName, alias, object.Name, object.External))
	}
	if len(secretNames) > 0 {
		existing, err := c.secrets.List(ctx, swarm.ListSecretsFilter{Names: secretNames})
		if err != nil {
			return result, faults.SanitizeError(err, "secrets: Docker metadata lookup failed")
		}
		for _, alias := range sortedKeys(desired.Secrets) {
			object := desired.Secrets[alias]
			name := resourceName(stackName, alias, object.Name, object.External)
			index := slices.IndexFunc(existing, func(r swarm.Secret) bool { return r.Name == name && r.ID != "" })
			if index < 0 {
				return result, fmt.Errorf("secrets.%s: required secret unavailable; reconcile it before conversion", alias)
			}
			result.Secrets[alias] = resources.ResolvedResource{ID: existing[index].ID, Name: existing[index].Name}
		}
	}
	return result, nil
}

func serviceObjects(container *dockerswarm.ContainerSpec, service compose.Service, resolved resources.Resolved) error {
	for _, object := range service.Configs {
		resource, ok := resolved.Configs[object.Source]
		if !ok {
			return fmt.Errorf("configs.%s: undefined config", object.Source)
		}
		target, uid, gid, mode := fileTarget(object, false)
		container.Configs = append(container.Configs, &dockerswarm.ConfigReference{
			ConfigID: resource.ID, ConfigName: resource.Name,
			File: &dockerswarm.ConfigReferenceFileTarget{Name: target, UID: uid, GID: gid, Mode: mode},
		})
	}
	for _, object := range service.Secrets {
		resource, ok := resolved.Secrets[object.Source]
		if !ok {
			return fmt.Errorf("secrets.%s: undefined secret", object.Source)
		}
		target, uid, gid, mode := fileTarget(object, true)
		container.Secrets = append(container.Secrets, &dockerswarm.SecretReference{
			SecretID: resource.ID, SecretName: resource.Name,
			File: &dockerswarm.SecretReferenceFileTarget{Name: target, UID: uid, GID: gid, Mode: mode},
		})
	}
	slices.SortStableFunc(container.Configs, func(a, b *dockerswarm.ConfigReference) int {
		return compareStrings(a.ConfigName, b.ConfigName)
	})
	slices.SortStableFunc(container.Secrets, func(a, b *dockerswarm.SecretReference) int {
		return compareStrings(a.SecretName, b.SecretName)
	})
	return nil
}

const defaultFileMode = 0o444

func fileTarget(object compose.ObjectRef, secret bool) (string, string, string, os.FileMode) {
	target := object.Target
	if target == "" || object.IsShortForm() {
		target = "/" + object.Source
		if secret {
			target = object.Source
		}
	}
	uid, gid := object.Uid, object.Gid
	if uid == "" {
		uid = "0"
	}
	if gid == "" {
		gid = "0"
	}
	mode := os.FileMode(defaultFileMode)
	if object.Mode != nil {
		mode = *object.Mode
	}
	return target, uid, gid, mode
}
