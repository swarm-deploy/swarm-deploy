// Package converter builds final Docker API specifications from a prepared Compose stack.
// It only reads resources through internal/swarm; reconciliation must precede conversion.
package converter

import (
	"context"
	"fmt"
	"maps"
	"slices"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// StackSpec contains fully resolved stack specifications, without resource payloads or credentials.
type StackSpec struct {
	// Networks includes declared networks and the implicit default network, sorted by Compose alias.
	Networks []NetworkSpec
	// Services contains final application service specifications, sorted by service name.
	Services []dockerswarm.ServiceSpec
}

// NetworkSpec describes a stack network and, after conversion, its existing Docker identity.
type NetworkSpec struct {
	// Alias is the Compose network key.
	Alias string
	// ID identifies the already reconciled Docker network; plans leave it empty.
	ID string
	// External marks networks that must not be managed as stack-owned resources.
	External bool
	// Spec describes the desired network; external networks contain only their resolved name.
	Spec swarm.CreateNetworkRequest
}

// Converter converts prepared stacks without modifying the input or Docker state.
type Converter struct {
	networks swarm.NetworkManager
	configs  swarm.ConfigManager
	secrets  swarm.SecretManager
}

// New creates a converter using the existing Swarm managers. All dependencies are mandatory.
func New(networks swarm.NetworkManager, configs swarm.ConfigManager, secrets swarm.SecretManager) *Converter {
	return &Converter{networks: networks, configs: configs, secrets: secrets}
}

// Validate checks whether desired can be converted without reading or changing Docker state.
func (*Converter) Validate(stackName string, desired compose.Compose) error {
	if err := validateStack(stackName, desired); err != nil {
		return err
	}
	// Run the same conversion against declared resource names. Docker identities
	// are resolved only in Convert; every local conversion error is caught here.
	networks := planNetworks(stackName, desired)
	objects := declaredObjects(stackName, desired)
	for _, service := range desired.Services {
		if _, err := convertService(stackName, service, desired.Volumes, networks, objects); err != nil {
			return fmt.Errorf("services.%s: %w", service.Name, err)
		}
	}
	return nil
}

// PlanNetworks returns the desired network specifications without reading or changing Docker state.
// The result uses the same naming, default-network, labels, and option conversion as Convert.
func (c *Converter) PlanNetworks(stackName string, desired compose.Compose) ([]NetworkSpec, error) {
	if err := c.Validate(stackName, desired); err != nil {
		return nil, err
	}
	return planNetworks(stackName, desired), nil
}

func planNetworks(stackName string, desired compose.Compose) []NetworkSpec {
	definitions := networkDefinitions(desired)
	result := make([]NetworkSpec, 0, len(definitions))
	for _, alias := range sortedKeys(definitions) {
		definition := definitions[alias]
		name := resourceName(stackName, alias, definition.Name, definition.External)
		result = append(result, NetworkSpec{
			Alias: alias, External: definition.External, Spec: networkSpec(stackName, name, definition),
		})
	}
	return result
}

func declaredObjects(stackName string, desired compose.Compose) resources.Resolved {
	objects := resources.Resolved{
		Configs: make(map[string]resources.ResolvedResource, len(desired.Configs)),
		Secrets: make(map[string]resources.ResolvedResource, len(desired.Secrets)),
	}
	for alias, object := range desired.Configs {
		objects.Configs[alias] = resources.ResolvedResource{
			Name: resourceName(stackName, alias, object.Name, object.External),
		}
	}
	for alias, object := range desired.Secrets {
		objects.Secrets[alias] = resources.ResolvedResource{
			Name: resourceName(stackName, alias, object.Name, object.External),
		}
	}
	return objects
}

// Convert builds final specifications. Missing resources produce an error and no partial result.
// Prepared Compose must already contain interpolated values, populated env files and rotated names.
// The caller must reconcile networks/configs/secrets before calling Convert, including on first deploy.
func (c *Converter) Convert(ctx context.Context, stackName string, desired compose.Compose) (*StackSpec, error) {
	if err := c.Validate(stackName, desired); err != nil {
		return nil, err
	}
	networks, err := c.convertNetworks(ctx, stackName, desired)
	if err != nil {
		return nil, err
	}
	objects, err := c.resolveObjects(ctx, stackName, desired)
	if err != nil {
		return nil, err
	}
	result := &StackSpec{Networks: networks, Services: make([]dockerswarm.ServiceSpec, 0, len(desired.Services))}
	services := slices.Clone(desired.Services)
	slices.SortFunc(services, func(a, b compose.Service) int { return compareStrings(a.Name, b.Name) })
	for _, service := range services {
		spec, convertErr := convertService(stackName, service, desired.Volumes, networks, objects)
		if convertErr != nil {
			return nil, fmt.Errorf("services.%s: %w", service.Name, convertErr)
		}
		result.Services = append(result.Services, spec)
	}
	return result, nil
}

func resourceName(stackName, alias, name string, external bool) string {
	if name != "" {
		return name
	}
	if external {
		return alias
	}
	return stackName + "_" + alias
}

func stackLabels(stackName string, labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels)+1)
	maps.Copy(result, labels)
	result[labelsdict.StackNamespace] = stackName
	return result
}

func sortedKeys[T any](values map[string]T) []string { return slices.Sorted(maps.Keys(values)) }

func compareStrings(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
