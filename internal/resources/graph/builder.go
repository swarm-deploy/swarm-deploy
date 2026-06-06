package graph

import (
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/resources/service/stype"
)

var dependencyEnvSuffixes = []string{
	"_HOST",
	"_ADDR",
	"_URL",
	"_ADDRESS",
	"_ENDPOINT",
}

// Builder builds service dependency graphs from persisted service metadata.
type Builder struct{}

// NewBuilder creates a graph builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Build constructs a graph with direct service dependencies resolved from environment variables.
func (b *Builder) Build(services []service.Info) Graph {
	serviceByName := make(map[string][]service.Info, len(services))

	for _, svc := range services {
		nodeName := b.serviceNodeName(svc)
		serviceByName[svc.Name] = append(serviceByName[svc.Name], svc)
		if nodeName != svc.Name {
			serviceByName[nodeName] = append(serviceByName[nodeName], svc)
		}
	}

	nodes := make([]Node, 0, len(services))
	for _, svc := range services {
		nodes = append(nodes, Node{
			Name:      b.serviceNodeName(svc),
			Kind:      kindFromServiceType(svc.Type),
			Endpoints: b.resolveEndpoints(svc),
			Depends:   b.resolveDependencies(svc, serviceByName),
		})
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	return Graph{Nodes: nodes}
}

func (b *Builder) resolveDependencies(
	source service.Info,
	serviceByName map[string][]service.Info,
) []string {
	if len(source.Environment) == 0 {
		return nil
	}

	dependencyNames := make(map[string]struct{})
	for envName, envValue := range source.Environment {
		if !b.isDependencyEnvName(envName) {
			continue
		}

		host := b.extractDependencyHost(envValue)
		if host == "" {
			continue
		}

		dependency, ok := b.resolveDependency(source, host, serviceByName)
		if !ok {
			continue
		}

		dependencyName := b.serviceNodeName(dependency)
		if dependencyName == b.serviceNodeName(source) {
			continue
		}

		dependencyNames[dependencyName] = struct{}{}
	}

	if len(dependencyNames) == 0 {
		return nil
	}

	dependencies := make([]string, 0, len(dependencyNames))
	for dependencyName := range dependencyNames {
		dependencies = append(dependencies, dependencyName)
	}

	sort.Slice(dependencies, func(i, j int) bool {
		return dependencies[i] < dependencies[j]
	})

	return dependencies
}

func (b *Builder) resolveEndpoints(svc service.Info) []string {
	if len(svc.WebRoutes) == 0 {
		return nil
	}

	endpoints := make([]string, 0, len(svc.WebRoutes))
	for _, route := range svc.WebRoutes {
		address := strings.TrimSpace(route.Address)
		port := strings.TrimSpace(route.Port)
		endpoints = append(endpoints, address+":"+port)
	}

	if len(endpoints) == 0 {
		return nil
	}

	return endpoints
}

func (b *Builder) isDependencyEnvName(name string) bool {
	upperName := strings.ToUpper(name)
	for _, suffix := range dependencyEnvSuffixes {
		if strings.HasSuffix(upperName, suffix) {
			return true
		}
	}

	return false
}

func (b *Builder) extractDependencyHost(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	if parsedURL, err := url.Parse(value); err == nil && parsedURL.Host != "" {
		return parsedURL.Hostname()
	}

	if parsedURL, err := url.Parse("//" + value); err == nil && parsedURL.Host != "" {
		return parsedURL.Hostname()
	}

	trimmed := b.trimAddressDecorators(value)
	if trimmed == "" {
		return ""
	}

	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}

	return trimmed
}

func (b *Builder) trimAddressDecorators(value string) string {
	trimmed := value
	if idx := strings.Index(trimmed, "@"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	if idx := strings.IndexAny(trimmed, "/?"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	if idx := strings.Index(trimmed, ":"); idx >= 0 && strings.Count(trimmed, ":") == 1 {
		trimmed = trimmed[:idx]
	}

	return strings.TrimSpace(trimmed)
}

func (b *Builder) resolveDependency(
	source service.Info,
	host string,
	serviceByName map[string][]service.Info,
) (service.Info, bool) {
	for _, candidate := range b.dependencyHostAliases(host) {
		if dependency, ok := b.findServiceInStack(serviceByName[candidate], source.Stack); ok {
			return dependency, true
		}
	}

	for _, candidate := range b.dependencyHostAliases(host) {
		dependencies := serviceByName[candidate]
		if len(dependencies) == 1 {
			return dependencies[0], true
		}
	}

	return service.Info{}, false
}

func (b *Builder) findServiceInStack(services []service.Info, stackName string) (service.Info, bool) {
	for _, svc := range services {
		if svc.Stack == stackName {
			return svc, true
		}
	}

	return service.Info{}, false
}

func (b *Builder) dependencyHostAliases(host string) []string {
	trimmedHost := strings.TrimSpace(host)
	if trimmedHost == "" {
		return nil
	}

	aliases := make([]string, 0)
	seen := map[string]struct{}{}
	add := func(alias string) {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return
		}
		if _, exists := seen[alias]; exists {
			return
		}

		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}

	add(trimmedHost)

	withoutTasksPrefix := strings.TrimPrefix(trimmedHost, "tasks.")
	add(withoutTasksPrefix)

	parts := strings.Split(withoutTasksPrefix, ".")
	if len(parts) >= 2 { //nolint:mnd // nn
		add(b.stackServiceKey(parts[1], parts[0]))
		add(b.stackServiceKey(parts[0], parts[1]))
		add(parts[0])
	}

	return aliases
}

func (b *Builder) serviceNodeName(svc service.Info) string {
	if svc.Stack == "" {
		return svc.Name
	}

	return b.stackServiceKey(svc.Stack, svc.Name)
}

func (b *Builder) stackServiceKey(stackName string, serviceName string) string {
	if stackName == "" {
		return serviceName
	}

	return stackName + "_" + serviceName
}

func kindFromServiceType(typ serviceType.Type) Kind {
	switch typ {
	case serviceType.Application:
		return KindApplication
	case serviceType.Monitoring:
		return KindMonitoring
	case serviceType.Delivery:
		return KindDelivery
	case serviceType.ReverseProxy:
		return KindReverseProxy
	case serviceType.DeploymentManagementSystem:
		return KindDeploymentManagementSystem
	case serviceType.Database:
		return KindDatabase
	case serviceType.SecretManager:
		return KindSecretManager
	case serviceType.CronManager:
		return KindCronManager
	default:
		return KindApplication
	}
}
