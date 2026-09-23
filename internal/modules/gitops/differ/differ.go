package differ

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/srvcomparator"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
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
	serviceComparator srvcomparator.Comparator
}

// New creates compose differ component.
func New() *Differ {
	sharedComparators := []srvcomparator.Comparator{
		&srvcomparator.CommandComparator{},
		&srvcomparator.EnvComparator{},
		&srvcomparator.ImageComparator{},
		&srvcomparator.NetworkComparator{},
		&srvcomparator.SecretComparator{},
		&srvcomparator.VolumeComparator{},
		&srvcomparator.ConfigComparator{},
	}

	serviceComparators := append([]srvcomparator.Comparator{}, sharedComparators...)
	serviceComparators = append(serviceComparators,
		&srvcomparator.PortComparator{},
		&srvcomparator.HealthcheckComparator{},
		&srvcomparator.DeployComparator{},
		&srvcomparator.CapabilitiesComparator{},
		&srvcomparator.LabelsComparator{},
		&srvcomparator.LoggingComparator{},
		&srvcomparator.EnvFileComparator{},
		srvcomparator.NewInitJobComparator(
			sharedComparators,
		),
	)

	return &Differ{
		serviceComparator: srvcomparator.NewComposeComparator(serviceComparators...),
	}
}

// Compare compares compose file snapshots and returns per-service changes.
func (d *Differ) Compare(composeFiles []ComposeFile) (diff.Diff, error) {
	result := diff.Diff{}
	for i, composeFile := range composeFiles {
		oldCompose, err := parseComposeFile(composeFile.OldComposeFile)
		if err != nil {
			return diff.Diff{}, fmt.Errorf("parse old compose file[%d] %q: %w", i, composeFile.ComposePath, err)
		}

		newCompose, err := parseComposeFile(composeFile.NewComposeFile)
		if err != nil {
			return diff.Diff{}, fmt.Errorf("parse new compose file[%d] %q: %w", i, composeFile.ComposePath, err)
		}

		composeDiff := d.CompareCompose(composeFile.StackName, oldCompose, newCompose)
		result.Services = append(result.Services, composeDiff.Services...)
		result.Networks = append(result.Networks, composeDiff.Networks...)
		result.Configs = append(result.Configs, composeDiff.Configs...)
		result.Secrets = append(result.Secrets, composeDiff.Secrets...)
		result.Volumes = append(result.Volumes, composeDiff.Volumes...)
	}

	sort.Slice(result.Services, func(i, j int) bool {
		left := result.Services[i]
		right := result.Services[j]
		if left.StackName == right.StackName {
			return left.ServiceName < right.ServiceName
		}
		return left.StackName < right.StackName
	})
	sortResourceDiffs(result.Networks)
	sortResourceDiffs(result.Configs)
	sortResourceDiffs(result.Secrets)
	sortResourceDiffs(result.Volumes)

	return result, nil
}

// CompareCompose compares already parsed compose models for one stack.
func (d *Differ) CompareCompose(stackName string, oldCompose, newCompose *compose.Compose) diff.Diff {
	if oldCompose == nil {
		oldCompose = &compose.Compose{}
	}
	if newCompose == nil {
		newCompose = &compose.Compose{}
	}

	return diff.Diff{
		Services: d.compareServices(stackName, oldCompose, newCompose),
		Networks: compareResources(stackName, oldCompose.Networks, newCompose.Networks),
		Configs:  compareResources(stackName, oldCompose.Configs, newCompose.Configs),
		Secrets:  compareResources(stackName, oldCompose.Secrets, newCompose.Secrets),
		Volumes:  compareResources(stackName, oldCompose.Volumes, newCompose.Volumes),
	}
}

func parseComposeFile(raw string) (*compose.Compose, error) {
	parsed, err := compose.Parse([]byte(raw))
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

func (d *Differ) compareServices(
	stackName string,
	oldCompose *compose.Compose,
	newCompose *compose.Compose,
) []diff.ServiceDiff {
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
		if !oldExists {
			oldService = compose.Service{}
		}
		if !newExists {
			newService = compose.Service{}
		}

		serviceDiff := d.compareService(stackName, serviceName, oldService, newService)
		serviceDiff.Added = !oldExists
		serviceDiff.Removed = !newExists
		serviceDiff.CalcHasChanges()
		if !serviceDiff.HasChanges {
			continue
		}
		serviceDiffs = append(serviceDiffs, serviceDiff)
	}

	return serviceDiffs
}

func compareResources[T any](stackName string, oldResources, newResources map[string]T) []diff.ResourceDiff {
	names := make([]string, 0, len(oldResources)+len(newResources))
	seen := make(map[string]struct{}, len(oldResources)+len(newResources))
	for name := range oldResources {
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for name := range newResources {
		if _, exists := seen[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	resourceDiffs := make([]diff.ResourceDiff, 0, len(names))
	for _, name := range names {
		oldResource, oldExists := oldResources[name]
		newResource, newExists := newResources[name]
		resourceDiff := diff.ResourceDiff{StackName: stackName, Name: name}
		switch {
		case !oldExists:
			resourceDiff.Added = true
		case !newExists:
			resourceDiff.Removed = true
		case !reflect.DeepEqual(oldResource, newResource):
			resourceDiff.Changed = true
		default:
			continue
		}
		resourceDiffs = append(resourceDiffs, resourceDiff)
	}

	return resourceDiffs
}

func sortResourceDiffs(resourceDiffs []diff.ResourceDiff) {
	sort.Slice(resourceDiffs, func(i, j int) bool {
		if resourceDiffs[i].StackName == resourceDiffs[j].StackName {
			return resourceDiffs[i].Name < resourceDiffs[j].Name
		}
		return resourceDiffs[i].StackName < resourceDiffs[j].StackName
	})
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

func (d *Differ) CompareService(stackName string, left compose.Service, right compose.Service) diff.ServiceDiff {
	return d.compareService(stackName, left.Name, left, right)
}

func (d *Differ) compareService(
	stackName string,
	serviceName string,
	oldService compose.Service,
	newService compose.Service,
) diff.ServiceDiff {
	serviceDiff := diff.ServiceDiff{
		ServiceName: serviceName,
		StackName:   stackName,
	}

	d.serviceComparator.Compare(oldService, newService, &serviceDiff)

	serviceDiff.CalcHasChanges()

	return serviceDiff
}
