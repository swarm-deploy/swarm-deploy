package differ

import (
	"fmt"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/gitops/differ/srvcomparator"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/differ/diff"
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
	return &Differ{
		serviceComparator: srvcomparator.NewComposeComparator(
			&srvcomparator.EnvComparator{},
			&srvcomparator.ImageComparator{},
			&srvcomparator.NetworkComparator{},
			&srvcomparator.SecretComparator{},
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

		serviceDiff, changed := d.compareService(stackName, serviceName, oldService, newService)
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

func (d *Differ) CompareService(stackName string, left compose.Service, right compose.Service) diff.ServiceDiff {
	sdiff, _ := d.compareService(stackName, left.Name, left, right)
	return sdiff
}

func (d *Differ) compareService(
	stackName string,
	serviceName string,
	oldService compose.Service,
	newService compose.Service,
) (diff.ServiceDiff, bool) {
	serviceDiff := diff.ServiceDiff{
		ServiceName: serviceName,
		StackName:   stackName,
	}

	d.serviceComparator.Compare(oldService, newService, &serviceDiff)

	changed := serviceDiff.Image != nil ||
		len(serviceDiff.Environment) > 0 ||
		len(serviceDiff.Networks) > 0 ||
		len(serviceDiff.Secrets) > 0

	serviceDiff.HasChanges = changed

	return serviceDiff, changed
}
