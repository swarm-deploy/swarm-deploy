package differ

import (
	"fmt"
	"sort"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/compose"
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

// Diff is a per-service compose changeset.
type Diff struct {
	// Services contains changed services.
	Services []ServiceDiff `json:"services"`
}

// ServiceDiff describes changed entities for one service.
type ServiceDiff struct {
	// ServiceName is a changed service name.
	ServiceName string `json:"serviceName"`
	// StackName is a stack where service belongs.
	StackName string `json:"stackName"`

	// Image contains image change details. Nil when image is unchanged.
	Image *ImageDiff `json:"image,omitempty"`
	// Environment contains changed service environment variables.
	Environment []EnvironmentDiff `json:"environment,omitempty"`
	// Networks contains changed service network attachments.
	Networks []NetworkDiff `json:"networks,omitempty"`
	// Secrets contains changed service secrets.
	Secrets []SecretDiff `json:"secrets,omitempty"`
}

// ImageDiff describes image value transition.
type ImageDiff struct {
	// Old is image before change.
	Old string `json:"old"`
	// New is image after change.
	New string `json:"new"`
}

// EnvironmentDiff describes one changed environment variable.
type EnvironmentDiff struct {
	// VarName is an environment variable name.
	VarName string `json:"varName"`
	// Value is a current variable value for add/change and old value for delete.
	Value string `json:"value"`
	// Added reports that variable is newly added.
	Added bool `json:"added,omitempty"`
	// Changed reports that variable value has changed.
	Changed bool `json:"changed,omitempty"`
	// Deleted reports that variable was removed.
	Deleted bool `json:"deleted,omitempty"`
}

// NetworkDiff describes one changed network connection.
type NetworkDiff struct {
	// Name is a network name.
	Name string `json:"name"`
	// Connected reports whether service is connected to this network after commit.
	Connected bool `json:"connected"`
}

// SecretDiff describes one changed secret mount.
type SecretDiff struct {
	// Name is a secret name.
	Name string `json:"name"`
	// MountFile is a target mount path in service container.
	MountFile string `json:"mountFile,omitempty"`
	// Added reports that secret mount was added.
	Added bool `json:"added,omitempty"`
	// Removed reports that secret mount was removed.
	Removed bool `json:"removed,omitempty"`
}

// Differ compares compose file snapshots.
type Differ struct{}

// New creates compose differ component.
func New() *Differ {
	return &Differ{}
}

// Compare compares compose file snapshots and returns per-service changes.
func (d *Differ) Compare(composeFiles []ComposeFile) (Diff, error) {
	serviceDiffs := make([]ServiceDiff, 0)
	for i, composeFile := range composeFiles {
		oldCompose, err := parseComposeFile(composeFile.OldComposeFile)
		if err != nil {
			return Diff{}, fmt.Errorf("parse old compose file[%d] %q: %w", i, composeFile.ComposePath, err)
		}

		newCompose, err := parseComposeFile(composeFile.NewComposeFile)
		if err != nil {
			return Diff{}, fmt.Errorf("parse new compose file[%d] %q: %w", i, composeFile.ComposePath, err)
		}

		serviceDiffs = append(serviceDiffs, compareServices(composeFile.StackName, oldCompose, newCompose)...)
	}

	sort.Slice(serviceDiffs, func(i, j int) bool {
		left := serviceDiffs[i]
		right := serviceDiffs[j]
		if left.StackName == right.StackName {
			return left.ServiceName < right.ServiceName
		}
		return left.StackName < right.StackName
	})

	return Diff{Services: serviceDiffs}, nil
}

func parseComposeFile(raw string) (*compose.File, error) {
	parsed, err := compose.Parse([]byte(raw))
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

func compareServices(stackName string, oldCompose *compose.File, newCompose *compose.File) []ServiceDiff {
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

	serviceDiffs := make([]ServiceDiff, 0, len(serviceNames))
	for _, serviceName := range serviceNames {
		oldService, oldExists := oldServices[serviceName]
		newService, newExists := newServices[serviceName]
		serviceDiff, changed := compareService(stackName, serviceName, oldService, oldExists, newService, newExists)
		if !changed {
			continue
		}
		serviceDiffs = append(serviceDiffs, serviceDiff)
	}

	return serviceDiffs
}

func mapServicesByName(composeFile *compose.File) map[string]compose.Service {
	if composeFile == nil {
		return map[string]compose.Service{}
	}

	services := make(map[string]compose.Service, len(composeFile.Services))
	for _, service := range composeFile.Services {
		services[service.Name] = service
	}

	return services
}

func compareService(
	stackName string,
	serviceName string,
	oldService compose.Service,
	oldExists bool,
	newService compose.Service,
	newExists bool,
) (ServiceDiff, bool) {
	serviceDiff := ServiceDiff{
		ServiceName: serviceName,
		StackName:   stackName,
	}

	oldImage := ""
	if oldExists {
		oldImage = strings.TrimSpace(oldService.Image)
	}
	newImage := ""
	if newExists {
		newImage = strings.TrimSpace(newService.Image)
	}
	if oldImage != newImage {
		serviceDiff.Image = &ImageDiff{
			Old: oldImage,
			New: newImage,
		}
	}

	oldEnvironment := map[string]string{}
	if oldExists {
		oldEnvironment = oldService.Environment
	}
	newEnvironment := map[string]string{}
	if newExists {
		newEnvironment = newService.Environment
	}
	serviceDiff.Environment = compareEnvironment(oldEnvironment, newEnvironment)

	oldNetworks := []string(nil)
	if oldExists {
		oldNetworks = oldService.Networks
	}
	newNetworks := []string(nil)
	if newExists {
		newNetworks = newService.Networks
	}
	serviceDiff.Networks = compareNetworks(oldNetworks, newNetworks)

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

func compareEnvironment(oldEnvironment map[string]string, newEnvironment map[string]string) []EnvironmentDiff {
	if oldEnvironment == nil {
		oldEnvironment = map[string]string{}
	}
	if newEnvironment == nil {
		newEnvironment = map[string]string{}
	}

	variableNames := map[string]struct{}{}
	for variableName := range oldEnvironment {
		variableNames[variableName] = struct{}{}
	}
	for variableName := range newEnvironment {
		variableNames[variableName] = struct{}{}
	}

	sortedVariableNames := mapKeys(variableNames)
	sort.Strings(sortedVariableNames)

	diffs := make([]EnvironmentDiff, 0, len(sortedVariableNames))
	for _, variableName := range sortedVariableNames {
		oldValue, oldExists := oldEnvironment[variableName]
		newValue, newExists := newEnvironment[variableName]

		switch {
		case !oldExists && newExists:
			diffs = append(diffs, EnvironmentDiff{
				VarName: variableName,
				Value:   newValue,
				Added:   true,
			})
		case oldExists && !newExists:
			diffs = append(diffs, EnvironmentDiff{
				VarName: variableName,
				Value:   oldValue,
				Deleted: true,
			})
		case oldExists && newExists && oldValue != newValue:
			diffs = append(diffs, EnvironmentDiff{
				VarName: variableName,
				Value:   newValue,
				Changed: true,
			})
		}
	}

	return diffs
}

func compareNetworks(oldNetworks []string, newNetworks []string) []NetworkDiff {
	oldSet := stringSliceToSet(oldNetworks)
	newSet := stringSliceToSet(newNetworks)

	networkNames := map[string]struct{}{}
	for networkName := range oldSet {
		networkNames[networkName] = struct{}{}
	}
	for networkName := range newSet {
		networkNames[networkName] = struct{}{}
	}

	sortedNetworkNames := mapKeys(networkNames)
	sort.Strings(sortedNetworkNames)

	diffs := make([]NetworkDiff, 0, len(sortedNetworkNames))
	for _, networkName := range sortedNetworkNames {
		_, oldExists := oldSet[networkName]
		_, newExists := newSet[networkName]

		switch {
		case !oldExists && newExists:
			diffs = append(diffs, NetworkDiff{Name: networkName, Connected: true})
		case oldExists && !newExists:
			diffs = append(diffs, NetworkDiff{Name: networkName, Connected: false})
		}
	}

	return diffs
}

func compareSecrets(oldSecrets []compose.ObjectRef, newSecrets []compose.ObjectRef) []SecretDiff {
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

	diffs := make([]SecretDiff, 0, len(sortedKeys))
	for _, key := range sortedKeys {
		oldRef, oldExists := oldSet[key]
		newRef, newExists := newSet[key]

		switch {
		case !oldExists && newExists:
			diffs = append(diffs, SecretDiff{
				Name:      newRef.Source,
				MountFile: newRef.Target,
				Added:     true,
			})
		case oldExists && !newExists:
			diffs = append(diffs, SecretDiff{
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

func stringSliceToSet(values []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, value := range values {
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
