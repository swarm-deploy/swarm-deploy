package stackloop

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type rotatedResourceCleaner struct {
	secrets swarm.SecretManager
	configs swarm.ConfigManager
	policy  config.SecretRotationCleanupSpec
	now     func() time.Time
}

type rotatedCleanupResult struct {
	Removed int
	Failed  int
}

type rotatedResource struct {
	id        string
	name      string
	createdAt time.Time
	labels    map[string]string
}

type rotatedResourceRemoval struct {
	typeName string
	resource rotatedResource
	remove   func(context.Context, string) error
}

func newRotatedResourceCleaner(
	secrets swarm.SecretManager,
	configs swarm.ConfigManager,
	policy config.SecretRotationCleanupSpec,
) *rotatedResourceCleaner {
	return &rotatedResourceCleaner{
		secrets: secrets,
		configs: configs,
		policy:  policy,
		now:     time.Now,
	}
}

func (c *rotatedResourceCleaner) clean(
	ctx context.Context,
	stackName string,
	desiredConfigs map[string]string,
	desiredSecrets map[string]string,
	services []swarm.StackService,
	configs []swarm.Config,
	secrets []swarm.Secret,
) rotatedCleanupResult {
	secretRefs, configRefs, refsErr := referencedResourceIDs(services)
	if refsErr != nil {
		slog.WarnContext(ctx, "[rotated-resource-cleaner] cleanup skipped: live references are incomplete",
			slog.String("stack", stackName),
			slog.Any("error", refsErr),
		)
		return rotatedCleanupResult{}
	}

	configPlan := c.planType(ctx, stackName, "config", mapConfigResources(configs), desiredConfigs, configRefs)
	secretPlan := c.planType(ctx, stackName, "secret", mapSecretResources(secrets), desiredSecrets, secretRefs)

	removals := make([]rotatedResourceRemoval, 0, len(configPlan)+len(secretPlan))
	for _, resource := range configPlan {
		removals = append(removals, rotatedResourceRemoval{
			typeName: "config", resource: resource, remove: c.configs.Remove,
		})
	}
	for _, resource := range secretPlan {
		removals = append(removals, rotatedResourceRemoval{
			typeName: "secret", resource: resource, remove: c.secrets.Remove,
		})
	}
	sort.Slice(removals, func(i, j int) bool {
		if removals[i].typeName != removals[j].typeName {
			return removals[i].typeName < removals[j].typeName
		}

		return removals[i].resource.id < removals[j].resource.id
	})

	return c.execute(ctx, stackName, removals)
}

func (c *rotatedResourceCleaner) execute(
	ctx context.Context,
	stackName string,
	removals []rotatedResourceRemoval,
) rotatedCleanupResult {
	result := rotatedCleanupResult{}
	for _, removal := range removals {
		if removeErr := removal.remove(ctx, removal.resource.id); removeErr != nil {
			result.Failed++
			slog.WarnContext(ctx, "[rotated-resource-cleaner] failed to remove rotated resource",
				slog.String("stack", stackName),
				slog.String("resource_type", removal.typeName),
				slog.String("resource_id", removal.resource.id),
				slog.String("resource_name", removal.resource.name),
				slog.Any("error", removeErr),
			)
			continue
		}

		result.Removed++
	}

	if result.Failed > 0 {
		slog.WarnContext(ctx, "[rotated-resource-cleaner] cleanup completed with failures",
			slog.String("stack", stackName),
			slog.Int("removed", result.Removed),
			slog.Int("failed", result.Failed),
		)
	} else if result.Removed > 0 {
		slog.InfoContext(ctx, "[rotated-resource-cleaner] cleanup completed",
			slog.String("stack", stackName),
			slog.Int("removed", result.Removed),
			slog.Int("failed", result.Failed),
		)
	}

	return result
}

func (c *rotatedResourceCleaner) planType(
	ctx context.Context,
	stackName string,
	typeName string,
	resources []rotatedResource,
	desired map[string]string,
	referenced map[string]struct{},
) []rotatedResource {
	plan, err := c.plan(stackName, resources, desired, referenced)
	if err == nil {
		return plan
	}

	slog.WarnContext(ctx, "[rotated-resource-cleaner] resource cleanup skipped",
		slog.String("stack", stackName),
		slog.String("resource_type", typeName),
		slog.Any("error", err),
	)

	return nil
}

func (c *rotatedResourceCleaner) plan(
	stackName string,
	resources []rotatedResource,
	desired map[string]string,
	referenced map[string]struct{},
) ([]rotatedResource, error) {
	groups, err := groupManagedResources(stackName, resources)
	if err != nil {
		return nil, err
	}

	remove := make([]rotatedResource, 0)
	for logicalName, generations := range groups {
		remove = append(remove, c.expiredGenerations(logicalName, generations, desired, referenced)...)
	}

	return remove, nil
}

func groupManagedResources(
	stackName string,
	resources []rotatedResource,
) (map[string][]rotatedResource, error) {
	groups := make(map[string][]rotatedResource)
	for _, resource := range resources {
		if resource.labels[labelsdict.RotatedResourceManagedLabelKey] != labelsdict.RotatedResourceManagedLabelValue {
			continue
		}
		if resource.labels[labelsdict.StackNamespace] != stackName {
			return nil, fmt.Errorf("managed resource %s has unexpected stack namespace", resource.name)
		}
		logicalName := strings.TrimSpace(resource.labels[labelsdict.RotatedResourceLogicalNameLabelKey])
		if logicalName == "" || resource.id == "" || resource.name == "" || resource.createdAt.IsZero() {
			return nil, fmt.Errorf("managed resource %s has incomplete ownership metadata", resource.name)
		}
		groups[logicalName] = append(groups[logicalName], resource)
	}

	return groups, nil
}

func (c *rotatedResourceCleaner) expiredGenerations(
	logicalName string,
	generations []rotatedResource,
	desired map[string]string,
	referenced map[string]struct{},
) []rotatedResource {
	sort.Slice(generations, func(i, j int) bool {
		if generations[i].createdAt.Equal(generations[j].createdAt) {
			return generations[i].id > generations[j].id
		}

		return generations[i].createdAt.After(generations[j].createdAt)
	})

	remove := make([]rotatedResource, 0)
	now := c.now()
	for i, resource := range generations {
		desiredName, existsInDesired := desired[logicalName]
		_, inUse := referenced[resource.id]
		protected := (existsInDesired && i < c.policy.KeepLast) ||
			(existsInDesired && desiredName == resource.name) ||
			inUse ||
			now.Sub(resource.createdAt) < c.policy.MinAge.Value
		if !protected {
			remove = append(remove, resource)
		}
	}

	return remove
}

func mapConfigResources(configs []swarm.Config) []rotatedResource {
	resources := make([]rotatedResource, 0, len(configs))
	for _, cfg := range configs {
		resources = append(resources, rotatedResource{
			id: cfg.ID, name: cfg.Name, createdAt: cfg.CreatedAt, labels: cfg.Labels,
		})
	}

	return resources
}

func mapSecretResources(secrets []swarm.Secret) []rotatedResource {
	resources := make([]rotatedResource, 0, len(secrets))
	for _, secret := range secrets {
		if secret.Driver != "" {
			continue
		}
		resources = append(resources, rotatedResource{
			id: secret.ID, name: secret.Name, createdAt: secret.CreatedAt, labels: secret.Labels,
		})
	}

	return resources
}

func referencedResourceIDs(services []swarm.StackService) (map[string]struct{}, map[string]struct{}, error) {
	secrets := make(map[string]struct{})
	configs := make(map[string]struct{})

	for _, service := range services {
		if err := addServiceSpecResourceRefs(service.FullName, &service.ServiceSpec, secrets, configs); err != nil {
			return nil, nil, err
		}
		if err := addServiceSpecResourceRefs(
			service.FullName+" previous spec",
			service.PreviousSpec,
			secrets,
			configs,
		); err != nil {
			return nil, nil, err
		}
	}

	return secrets, configs, nil
}

func addServiceSpecResourceRefs(
	serviceName string,
	spec *dockerswarm.ServiceSpec,
	secrets map[string]struct{},
	configs map[string]struct{},
) error {
	if spec == nil || spec.TaskTemplate.ContainerSpec == nil {
		return nil
	}

	containerSpec := spec.TaskTemplate.ContainerSpec
	for _, ref := range containerSpec.Secrets {
		if ref == nil {
			continue
		}
		if ref.SecretID == "" {
			return fmt.Errorf("service %s has a secret reference without SecretID", serviceName)
		}
		secrets[ref.SecretID] = struct{}{}
	}
	for _, ref := range containerSpec.Configs {
		if ref == nil {
			continue
		}
		if ref.ConfigID == "" {
			return fmt.Errorf("service %s has a config reference without ConfigID", serviceName)
		}
		configs[ref.ConfigID] = struct{}{}
	}

	return nil
}
