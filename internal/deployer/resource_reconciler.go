package deployer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/docker/docker/api/types/filters"
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
	sourceComposePath string,
	configs compose.Configs,
	secrets compose.Secrets,
) (resolvedResources, error) {
	resolvedConfigs, err := r.reconcileConfigs(ctx, stackName, configs)
	if err != nil {
		return resolvedResources{}, err
	}

	resolvedSecrets, err := r.reconcileSecrets(ctx, stackName, sourceComposePath, secrets)
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
	objects compose.Configs,
) (map[string]ResolvedResource, error) {
	resolved := make(map[string]ResolvedResource, len(objects))
	if len(objects) == 0 {
		return resolved, nil
	}

	existing, listErr := r.dockerClient.ConfigList(ctx, dockerswarm.ConfigListOptions{
		Filters: configNameFilters(stackName, objects),
	})
	if listErr != nil {
		return nil, fmt.Errorf("list configs: %w", listErr)
	}
	existingByName := configsByName(existing)

	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerConfigName(stackName, alias, object)

		if cfg, ok := existingByName[name]; ok {
			resolved[alias] = ResolvedResource{ID: cfg.ID, Name: cfg.Spec.Name}
			continue
		}
		if object.External {
			return nil, fmt.Errorf("external config %s does not exist", name)
		}

		spec := dockerswarm.ConfigSpec{
			Annotations: dockerswarm.Annotations{
				Name:   name,
				Labels: resourceLabels(stackName, object.Labels.Map),
			},
			Data: object.Data,
		}
		if object.TemplateDriver != "" {
			spec.Templating = &dockerswarm.Driver{Name: object.TemplateDriver}
		}

		created, err := r.dockerClient.ConfigCreate(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("create config %s: %w", name, err)
		}

		resolved[alias] = ResolvedResource{ID: created.ID, Name: name}
		existingByName[name] = dockerswarm.Config{ID: created.ID, Spec: spec}
	}

	return resolved, nil
}

func (r *resourceReconciler) reconcileSecrets(
	ctx context.Context,
	stackName string,
	sourceComposePath string,
	objects compose.Secrets,
) (map[string]ResolvedResource, error) {
	resolved := make(map[string]ResolvedResource, len(objects))
	if len(objects) == 0 {
		return resolved, nil
	}

	existing, listErr := r.dockerClient.SecretList(ctx, dockerswarm.SecretListOptions{
		Filters: secretNameFilters(stackName, objects),
	})
	if listErr != nil {
		return nil, fmt.Errorf("list secrets: %w", listErr)
	}
	existingByName := secretsByName(existing)

	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerSecretName(stackName, alias, object)

		if secret, ok := existingByName[name]; ok {
			resolved[alias] = ResolvedResource{ID: secret.ID, Name: secret.Spec.Name}
			continue
		}
		if object.External {
			return nil, fmt.Errorf("external secret %s does not exist", name)
		}

		var data []byte
		if object.Driver == "" {
			var readErr error
			data, readErr = readSecret(sourceComposePath, object)
			if readErr != nil {
				return nil, fmt.Errorf("read secret %s: %w", name, readErr)
			}
		}

		spec := dockerswarm.SecretSpec{
			Annotations: dockerswarm.Annotations{
				Name:   name,
				Labels: resourceLabels(stackName, object.Labels.Map),
			},
			Data: data,
		}
		if object.Driver != "" {
			spec.Driver = &dockerswarm.Driver{Name: object.Driver, Options: object.DriverOpts}
		}
		if object.TemplateDriver != "" {
			spec.Templating = &dockerswarm.Driver{Name: object.TemplateDriver}
		}

		created, err := r.dockerClient.SecretCreate(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("create secret %s: %w", name, err)
		}

		resolved[alias] = ResolvedResource{ID: created.ID, Name: name}
		existingByName[name] = dockerswarm.Secret{ID: created.ID, Spec: spec}
	}

	return resolved, nil
}

func configsByName(configs []dockerswarm.Config) map[string]dockerswarm.Config {
	byName := make(map[string]dockerswarm.Config, len(configs))
	for _, config := range configs {
		byName[config.Spec.Name] = config
	}
	return byName
}

func secretsByName(secrets []dockerswarm.Secret) map[string]dockerswarm.Secret {
	byName := make(map[string]dockerswarm.Secret, len(secrets))
	for _, secret := range secrets {
		byName[secret.Spec.Name] = secret
	}
	return byName
}

func configNameFilters(stackName string, objects compose.Configs) filters.Args {
	result := filters.NewArgs()
	for _, alias := range sortedObjectAliases(objects) {
		result.Add("name", dockerConfigName(stackName, alias, objects[alias]))
	}
	return result
}

func secretNameFilters(stackName string, objects compose.Secrets) filters.Args {
	result := filters.NewArgs()
	for _, alias := range sortedObjectAliases(objects) {
		result.Add("name", dockerSecretName(stackName, alias, objects[alias]))
	}
	return result
}

func resourceLabels(stackName string, labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels)+1)
	for key, value := range labels {
		result[key] = value
	}
	result[labelsdict.StackNamespace] = stackName
	return result
}

func sortedObjectAliases[T any, Objects ~map[string]*T](objects Objects) []string {
	aliases := make([]string, 0, len(objects))
	for alias := range objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	return aliases
}

func dockerConfigName(stackName, alias string, object *compose.Config) string {
	if object.Name != "" {
		return object.Name
	}
	if object.External {
		return alias
	}

	return stackName + "_" + alias
}

func dockerSecretName(stackName, alias string, object *compose.Secret) string {
	if object.Name != "" {
		return object.Name
	}
	if object.External {
		return alias
	}

	return stackName + "_" + alias
}

func readSecret(sourceComposePath string, object *compose.Secret) ([]byte, error) {
	if object.File == "" {
		return nil, fmt.Errorf("file is required")
	}

	path := object.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(sourceComposePath), path)
	}

	return os.ReadFile(path)
}
