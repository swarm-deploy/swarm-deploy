package deployer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type resolvedResources struct {
	configs map[string]ResolvedResource
	secrets map[string]ResolvedResource
}

type resourceReconciler struct {
	configs swarm.ConfigManager
	secrets swarm.SecretManager
}

func newResourceReconciler(swarmService *swarm.Swarm) *resourceReconciler {
	return &resourceReconciler{
		configs: swarmService.Configs,
		secrets: swarmService.Secrets,
	}
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

	existing, listErr := r.configs.List(ctx, swarm.ListConfigsFilter{Names: configNames(stackName, objects)})
	if listErr != nil {
		return nil, fmt.Errorf("list configs: %w", listErr)
	}
	existingByName := configsByName(existing)

	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerConfigName(stackName, alias, object)

		if cfg, ok := existingByName[name]; ok {
			resolved[alias] = ResolvedResource{ID: cfg.ID, Name: cfg.Name}
			continue
		}
		if object.External {
			return nil, fmt.Errorf("external config %s does not exist", name)
		}

		createdID, err := r.configs.Create(ctx, swarm.CreateConfigRequest{
			Name:           name,
			Labels:         resourceLabels(stackName, object.Labels.Map),
			Data:           object.Data,
			TemplateDriver: object.TemplateDriver,
		})
		if err != nil {
			return nil, fmt.Errorf("create config %s: %w", name, err)
		}

		resolved[alias] = ResolvedResource{ID: createdID, Name: name}
		existingByName[name] = swarm.Config{ID: createdID, Name: name}
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

	existing, listErr := r.secrets.List(ctx, swarm.ListSecretsFilter{Names: secretNames(stackName, objects)})
	if listErr != nil {
		return nil, fmt.Errorf("list secrets: %w", listErr)
	}
	existingByName := secretsByName(existing)

	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerSecretName(stackName, alias, object)

		if secret, ok := existingByName[name]; ok {
			resolved[alias] = ResolvedResource{ID: secret.ID, Name: secret.Name}
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

		createdID, err := r.secrets.Create(ctx, swarm.CreateSecretRequest{
			Name:           name,
			Labels:         resourceLabels(stackName, object.Labels.Map),
			Data:           data,
			Driver:         object.Driver,
			DriverOptions:  object.DriverOpts,
			TemplateDriver: object.TemplateDriver,
		})
		if err != nil {
			return nil, fmt.Errorf("create secret %s: %w", name, err)
		}

		resolved[alias] = ResolvedResource{ID: createdID, Name: name}
		existingByName[name] = swarm.Secret{ID: createdID, Name: name}
	}

	return resolved, nil
}

func configsByName(configs []swarm.Config) map[string]swarm.Config {
	byName := make(map[string]swarm.Config, len(configs))
	for _, config := range configs {
		byName[config.Name] = config
	}
	return byName
}

func secretsByName(secrets []swarm.Secret) map[string]swarm.Secret {
	byName := make(map[string]swarm.Secret, len(secrets))
	for _, secret := range secrets {
		byName[secret.Name] = secret
	}
	return byName
}

func configNames(stackName string, objects compose.Configs) []string {
	result := make([]string, 0, len(objects))
	for _, alias := range sortedObjectAliases(objects) {
		result = append(result, dockerConfigName(stackName, alias, objects[alias]))
	}
	return result
}

func secretNames(stackName string, objects compose.Secrets) []string {
	result := make([]string, 0, len(objects))
	for _, alias := range sortedObjectAliases(objects) {
		result = append(result, dockerSecretName(stackName, alias, objects[alias]))
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
