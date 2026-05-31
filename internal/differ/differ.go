package differ

import (
	"fmt"
	"sort"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/differ/comparators"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

// ComposeFile contains old/new compose snapshots for one stack.
type ComposeFile struct {
	// StackName is a stack where compose file belongs.
	StackName string
	// ComposePath is a compose file path in repository.
	ComposePath string
	// OldComposeFile is compose YAML content before commit.
	OldComposeFile string
	// NewComposeFile is compose YAML content after commit.
	NewComposeFile string
}

// Differ compares compose file snapshots.
type Differ struct {
	serviceComparator comparators.ServiceComparator
}

// New creates compose differ component.
func New() *Differ {
	return &Differ{
		serviceComparator: comparators.NewComposeServiceComparator(
			&comparators.ServiceEnvComparator{},
			&comparators.ServiceImageComparator{},
		),
	}
}

// Compare compares compose file snapshots and returns per-service changes.
func (d *Differ) Compare(composeFiles []ComposeFile) (diff.Diff, error) {
	serviceDiffs := make([]diff.ServiceDiff, 0)
	for i, composeFile := range composeFiles {
		oldCompose, err := parseComposeFile(composeFile.OldComposeFile)
		if err != nil {
			return diff.Diff{}, fmt.Errorf("parse old compose file[%d] %q: %w", i, composeFile.ComposePath, err)
		}

		newCompose, err := parseComposeFile(composeFile.NewComposeFile)
		if err != nil {
			return diff.Diff{}, fmt.Errorf("parse new compose file[%d] %q: %w", i, composeFile.ComposePath, err)
		}

		serviceDiffs = append(serviceDiffs, d.compareServices(composeFile.StackName, oldCompose, newCompose)...)
	}

	sort.Slice(serviceDiffs, func(i, j int) bool {
		left := serviceDiffs[i]
		right := serviceDiffs[j]
		if left.StackName == right.StackName {
			return left.ServiceName < right.ServiceName
		}
		return left.StackName < right.StackName
	})

	return diff.Diff{Services: serviceDiffs}, nil
}

func parseComposeFile(raw string) (*compose.Compose, error) {
	parsed, err := compose.Parse([]byte(raw))
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

func (d *Differ) compareServices(stackName string, oldCompose *compose.Compose, newCompose *compose.Compose) []diff.ServiceDiff {
	oldServices := mapServicesByName(oldCompose)
	newServices := mapServicesByName(newCompose)

	serviceNames := make([]string, 0, len(oldServices)+len(newServices))
	seen := map[string]struct{}{}
	for serviceName := range oldServices {
		if _, exists := seen[serviceName]; exists {
			continue
		}
		seen[serviceName] = struct{}{}
		serviceNames = append(serviceNames, serviceName)
	}
	for serviceName := range newServices {
		if _, exists := seen[serviceName]; exists {
			continue
		}
		seen[serviceName] = struct{}{}
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)

	serviceDiffs := make([]diff.ServiceDiff, 0, len(serviceNames))
	for _, serviceName := range serviceNames {
		oldService, oldExists := oldServices[serviceName]
		newService, newExists := newServices[serviceName]
		serviceDiff, changed := d.compareService(stackName, serviceName, oldService, oldExists, newService, newExists)
		if !changed {
			continue
		}
		serviceDiffs = append(serviceDiffs, serviceDiff)
	}

	return serviceDiffs
}

func mapServicesByName(composeFile *compose.Compose) map[string]compose.Service {
	if composeFile == nil {
		return map[string]compose.Service{}
	}

	services := make(map[string]compose.Service, len(composeFile.Services))
	for _, service := range composeFile.Services {
		services[service.Name] = service
	}

	return services
}

func (d *Differ) compareService(
	stackName string,
	serviceName string,
	oldService compose.Service,
	oldExists bool,
	newService compose.Service,
	newExists bool,
) (diff.ServiceDiff, bool) {
	serviceDiff := diff.ServiceDiff{
		ServiceName: serviceName,
		StackName:   stackName,
	}

	d.serviceComparator.Compare(oldService, newService, &serviceDiff)

	serviceDiff.Networks = compareNetworks(oldService.Networks, newService.Networks)

	oldSecrets := []compose.ObjectRef(nil)
	if oldExists {
		oldSecrets = oldService.Secrets
	}
	newSecrets := []compose.ObjectRef(nil)
	if newExists {
		newSecrets = newService.Secrets
	}
	serviceDiff.Secrets = compareSecrets(oldSecrets, newSecrets)

	changed := serviceDiff.Image != nil ||
		len(serviceDiff.Environment) > 0 ||
		len(serviceDiff.Networks) > 0 ||
		len(serviceDiff.Secrets) > 0

	return serviceDiff, changed
}

func compareNetworks(oldNetworks *compose.ServiceNetworks, newNetworks *compose.ServiceNetworks) []diff.NetworkDiff {
	oldSet := networkAliasesSet(oldNetworks)
	newSet := networkAliasesSet(newNetworks)

	networkNames := map[string]struct{}{}
	for _, networkName := range oldNetworks.GetAliases() {
		networkNames[networkName] = struct{}{}
	}
	for _, networkName := range newNetworks.GetAliases() {
		networkNames[networkName] = struct{}{}
	}

	sortedNetworkNames := mapKeys(networkNames)
	sort.Strings(sortedNetworkNames)

	diffs := make([]diff.NetworkDiff, 0, len(sortedNetworkNames))
	for _, networkName := range sortedNetworkNames {
		_, oldExists := oldSet[networkName]
		_, newExists := newSet[networkName]

		switch {
		case !oldExists && newExists:
			diffs = append(diffs, diff.NetworkDiff{Name: networkName, Connected: true})
		case oldExists && !newExists:
			diffs = append(diffs, diff.NetworkDiff{Name: networkName, Connected: false})
		}
	}

	return diffs
}

func compareSecrets(oldSecrets []compose.ObjectRef, newSecrets []compose.ObjectRef) []diff.SecretDiff {
	oldSet := mapSecretRefs(oldSecrets)
	newSet := mapSecretRefs(newSecrets)

	keys := map[string]struct{}{}
	for key := range oldSet {
		keys[key] = struct{}{}
	}
	for key := range newSet {
		keys[key] = struct{}{}
	}

	sortedKeys := mapKeys(keys)
	sort.Strings(sortedKeys)

	diffs := make([]diff.SecretDiff, 0, len(sortedKeys))
	for _, key := range sortedKeys {
		oldRef, oldExists := oldSet[key]
		newRef, newExists := newSet[key]

		switch {
		case !oldExists && newExists:
			diffs = append(diffs, diff.SecretDiff{
				Name:      newRef.Source,
				MountFile: newRef.Target,
				Added:     true,
			})
		case oldExists && !newExists:
			diffs = append(diffs, diff.SecretDiff{
				Name:      oldRef.Source,
				MountFile: oldRef.Target,
				Removed:   true,
			})
		}
	}

	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Name == diffs[j].Name {
			if diffs[i].MountFile == diffs[j].MountFile {
				return boolScore(diffs[i].Added) > boolScore(diffs[j].Added)
			}
			return diffs[i].MountFile < diffs[j].MountFile
		}
		return diffs[i].Name < diffs[j].Name
	})

	return diffs
}

func mapSecretRefs(secrets []compose.ObjectRef) map[string]compose.ObjectRef {
	set := map[string]compose.ObjectRef{}
	for _, secret := range secrets {
		key := secret.Source + "\x00" + secret.Target
		set[key] = secret
	}
	return set
}

func networkAliasesSet(networks *compose.ServiceNetworks) map[string]struct{} {
	if networks == nil {
		return map[string]struct{}{}
	}

	set := map[string]struct{}{}
	for _, value := range networks.Aliases {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			continue
		}
		set[trimmedValue] = struct{}{}
	}
	return set
}

func mapKeys[T any](source map[string]T) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	return keys
}

func boolScore(v bool) int {
	if v {
		return 1
	}
	return 0
}
