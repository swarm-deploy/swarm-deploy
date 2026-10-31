// Package resources reconciles Compose configs and secrets with Docker Swarm.
package resources

import (
	"context"
	"fmt"
	"maps"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Resolved contains Docker resource references indexed by Compose alias.
type Resolved struct {
	Configs map[string]ResolvedResource
	Secrets map[string]ResolvedResource
}

// ResolvedResource identifies an existing Docker Swarm config or secret.
type ResolvedResource struct {
	ID   string
	Name string
}

// Reconciler creates or resolves the configs and secrets required by a deployment.
type Reconciler struct {
	configs swarm.ConfigManager
	secrets swarm.SecretManager
}

// New creates a resource reconciler from the Swarm service facade.
func New(swarmService *swarm.Swarm) *Reconciler {
	return &Reconciler{
		configs: swarmService.Configs,
		secrets: swarmService.Secrets,
	}
}

// NewWithManagers creates a resource reconciler from its direct dependencies.
func NewWithManagers(configs swarm.ConfigManager, secrets swarm.SecretManager) *Reconciler {
	return &Reconciler{configs: configs, secrets: secrets}
}

// Reconcile resolves configs first and secrets second, preserving the existing ordering.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	stackName string,
	configs compose.Configs,
	secrets compose.Secrets,
	secretData map[string][]byte,
) (Resolved, error) {
	resolvedConfigs, err := r.reconcileConfigs(ctx, stackName, configs)
	if err != nil {
		return Resolved{}, err
	}

	resolvedSecrets, err := r.reconcileSecrets(ctx, stackName, secrets, secretData)
	if err != nil {
		return Resolved{}, err
	}

	return Resolved{
		Configs: resolvedConfigs,
		Secrets: resolvedSecrets,
	}, nil
}

func (r *Reconciler) reconcileConfigs(
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

func (r *Reconciler) reconcileSecrets(
	ctx context.Context,
	stackName string,
	objects compose.Secrets,
	secretData map[string][]byte,
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
	if err := validateSecretOwnership(stackName, objects, existingByName); err != nil {
		return nil, err
	}

	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerSecretName(stackName, alias, object)

		if secret, ok := existingByName[name]; ok {
			labels := resourceLabels(stackName, object.Labels.Map)
			if !object.External && !maps.Equal(secret.Labels, labels) {
				if err := r.secrets.UpdateLabels(ctx, secret.ID, labels); err != nil {
					return nil, fmt.Errorf("update secret %s: %w", name, faults.SanitizeError(err, "Docker API request failed"))
				}
				secret.Labels = labels
				existingByName[name] = secret
			}
			resolved[alias] = ResolvedResource{ID: secret.ID, Name: secret.Name}
			continue
		}
		if object.External {
			return nil, fmt.Errorf("external secret %s does not exist", name)
		}

		createdID, err := r.secrets.Create(ctx, swarm.CreateSecretRequest{
			Name:           name,
			Labels:         resourceLabels(stackName, object.Labels.Map),
			Data:           secretData[alias],
			Driver:         object.Driver,
			DriverOptions:  object.DriverOpts,
			TemplateDriver: object.TemplateDriver,
		})
		if err != nil {
			// Remote errors may echo secret payloads, regardless of their source format.
			return nil, fmt.Errorf("create secret %s: %w", name, faults.SanitizeError(err, "Docker API request failed"))
		}

		resolved[alias] = ResolvedResource{ID: createdID, Name: name}
		existingByName[name] = swarm.Secret{ID: createdID, Name: name, Labels: resourceLabels(stackName, object.Labels.Map)}
	}

	return resolved, nil
}

// Validate the entire batch before adopting resources or changing any labels.
func validateSecretOwnership(stackName string, objects compose.Secrets, existing map[string]swarm.Secret) error {
	for _, alias := range sortedObjectAliases(objects) {
		object := objects[alias]
		name := dockerSecretName(stackName, alias, object)
		if secret, exists := existing[name]; exists && !object.External &&
			secret.Labels[labelsdict.StackNamespace] != stackName {
			return fmt.Errorf("secret %s ownership conflict: expected stack %s", name, stackName)
		}
	}
	return nil
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
