package deployer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

type resolvedResources struct {
	configs map[string]ResolvedResource
	secrets map[string]ResolvedResource
}

type resourceReconciler struct {
	dockerClient *client.Client
}

func newResourceReconciler(dockerClient *client.Client) *resourceReconciler {
	return &resourceReconciler{dockerClient: dockerClient}
}

func (r *resourceReconciler) Reconcile(
	ctx context.Context,
	stackName string,
	composePath string,
	configs compose.SharedObjects,
	secrets compose.SharedObjects,
) (resolvedResources, error) {
	resolvedConfigs, err := r.reconcileConfigs(ctx, stackName, composePath, configs)
	if err != nil {
		return resolvedResources{}, err
	}

	resolvedSecrets, err := r.reconcileSecrets(ctx, stackName, composePath, secrets)
	if err != nil {
		return resolvedResources{}, err
	}

	return resolvedResources{
		configs: resolvedConfigs,
		secrets: resolvedSecrets,
	}, nil
}

func (r *resourceReconciler) reconcileConfigs(
	ctx context.Context,
	stackName string,
	composePath string,
	objects compose.SharedObjects,
) (map[string]ResolvedResource, error) {
	resolved := make(map[string]ResolvedResource, len(objects))
	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerResourceName(stackName, alias, object)

		cfg, _, err := r.dockerClient.ConfigInspectWithRaw(ctx, name)
		switch {
		case err == nil:
			resolved[alias] = ResolvedResource{ID: cfg.ID, Name: cfg.Spec.Name}
			continue
		case !cerrdefs.IsNotFound(err):
			return nil, fmt.Errorf("inspect config %s: %w", name, err)
		case object.External:
			return nil, fmt.Errorf("external config %s does not exist: %w", name, err)
		}

		data, err := readSharedObject(composePath, object)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", name, err)
		}

		created, err := r.dockerClient.ConfigCreate(ctx, dockerswarm.ConfigSpec{
			Annotations: dockerswarm.Annotations{
				Name: name,
				Labels: map[string]string{
					labelsdict.StackNamespace: stackName,
				},
			},
			Data: data,
		})
		if err != nil {
			return nil, fmt.Errorf("create config %s: %w", name, err)
		}

		resolved[alias] = ResolvedResource{ID: created.ID, Name: name}
	}

	return resolved, nil
}

func (r *resourceReconciler) reconcileSecrets(
	ctx context.Context,
	stackName string,
	composePath string,
	objects compose.SharedObjects,
) (map[string]ResolvedResource, error) {
	resolved := make(map[string]ResolvedResource, len(objects))
	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerResourceName(stackName, alias, object)

		secret, _, err := r.dockerClient.SecretInspectWithRaw(ctx, name)
		switch {
		case err == nil:
			resolved[alias] = ResolvedResource{ID: secret.ID, Name: secret.Spec.Name}
			continue
		case !cerrdefs.IsNotFound(err):
			return nil, fmt.Errorf("inspect secret %s: %w", name, err)
		case object.External:
			return nil, fmt.Errorf("external secret %s does not exist: %w", name, err)
		}

		data, err := readSharedObject(composePath, object)
		if err != nil {
			return nil, fmt.Errorf("read secret %s: %w", name, err)
		}

		spec := dockerswarm.SecretSpec{
			Annotations: dockerswarm.Annotations{
				Name: name,
				Labels: map[string]string{
					labelsdict.StackNamespace: stackName,
				},
			},
			Data: data,
		}
		if object.Driver != "" {
			spec.Driver = &dockerswarm.Driver{Name: object.Driver}
		}

		created, err := r.dockerClient.SecretCreate(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("create secret %s: %w", name, err)
		}

		resolved[alias] = ResolvedResource{ID: created.ID, Name: name}
	}

	return resolved, nil
}

func sortedObjectAliases(objects compose.SharedObjects) []string {
	aliases := make([]string, 0, len(objects))
	for alias := range objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	return aliases
}

func dockerResourceName(stackName, alias string, object *compose.SharedObject) string {
	if object.Name != "" {
		return object.Name
	}
	if object.External {
		return alias
	}

	return stackName + "_" + alias
}

func readSharedObject(composePath string, object *compose.SharedObject) ([]byte, error) {
	if object.Driver != "" {
		return nil, nil
	}
	if object.File == "" {
		return nil, fmt.Errorf("file is required")
	}

	path := object.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(composePath), path)
	}

	return os.ReadFile(path)
}
