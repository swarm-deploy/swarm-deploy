package graph

import (
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
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
	serviceByFullName := make(map[string]service.Info, len(services))
	serviceByStackAndName := make(map[string]service.Info, len(services))

	for _, svc := range services {
		nodeName := b.serviceNodeName(svc)
		serviceByFullName[nodeName] = svc
		serviceByStackAndName[b.stackServiceKey(svc.Stack, svc.Name)] = svc
	}

	nodes := make([]Node, 0, len(services))
	for _, svc := range services {
		nodes = append(nodes, Node{
			Name:    b.serviceNodeName(svc),
			Kind:    KindService,
			Depends: b.resolveDependencies(svc, serviceByFullName, serviceByStackAndName),
		})
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	return Graph{Nodes: nodes}
}

func (b *Builder) resolveDependencies(
	source service.Info,
	serviceByFullName map[string]service.Info,
	serviceByStackAndName map[string]service.Info,
) []Node {
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

		dependency, ok := b.resolveDependency(source, host, serviceByFullName, serviceByStackAndName)
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

	dependencies := make([]Node, 0, len(dependencyNames))
	for dependencyName := range dependencyNames {
		dependencies = append(dependencies, Node{
			Name: dependencyName,
			Kind: KindService,
		})
	}

	sort.Slice(dependencies, func(i, j int) bool {
		return dependencies[i].Name < dependencies[j].Name
	})

	return dependencies
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
	serviceByFullName map[string]service.Info,
	serviceByStackAndName map[string]service.Info,
) (service.Info, bool) {
	if dependency, ok := serviceByStackAndName[b.stackServiceKey(source.Stack, host)]; ok {
		return dependency, true
	}

	dependency, ok := serviceByFullName[host]
	return dependency, ok
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
