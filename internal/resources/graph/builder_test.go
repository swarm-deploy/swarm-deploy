package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	"github.com/swarm-deploy/webroute"
)

func TestBuilderBuild(t *testing.T) {
	tests := []struct {
		name     string
		services []service.Info
		expected map[string]graphNodeSnapshot
	}{
		{
			name: "builds dependencies from supported env suffixes",
			services: []service.Info{
				{
					Stack: "prod",
					Name:  "api",
					WebRoutes: []webroute.Route{
						{Port: "443", Address: "api.example.com"},
						{Port: "8443", Address: "api.example.com/internal"},
					},
					Environment: map[string]string{
						"DB_HOST":          "db",
						"REDIS_ADDR":       "redis:6379",
						"PAYMENTS_URL":     "http://payments:8080/v1/internal",
						"SEARCH_ADDRESS":   "search",
						"AUTH_ENDPOINT":    "https://auth/api",
						"IGNORED_VARIABLE": "worker",
					},
				},
				{Stack: "prod", Name: "db"},
				{Stack: "prod", Name: "redis"},
				{Stack: "prod", Name: "payments"},
				{Stack: "prod", Name: "search"},
				{Stack: "prod", Name: "auth"},
				{Stack: "prod", Name: "worker"},
			},
			expected: map[string]graphNodeSnapshot{
				"prod_api": {
					Endpoints: []string{"api.example.com:443", "api.example.com/internal:8443"},
					Depends:   []string{"prod_auth", "prod_db", "prod_payments", "prod_redis", "prod_search"},
				},
				"prod_auth":     {},
				"prod_db":       {},
				"prod_payments": {},
				"prod_redis":    {},
				"prod_search":   {},
				"prod_worker":   {},
			},
		},
		{
			name: "prefers same stack name and supports full service names",
			services: []service.Info{
				{
					Stack: "blue",
					Name:  "gateway",
					Environment: map[string]string{
						"API_HOST":    "api",
						"WORKER_ADDR": "green_worker:9000",
					},
				},
				{
					Stack: "blue",
					Name:  "api",
					WebRoutes: []webroute.Route{
						{Port: "443", Address: "blue-api.example.com"},
					},
				},
				{Stack: "green", Name: "api"},
				{
					Stack: "green",
					Name:  "worker",
					WebRoutes: []webroute.Route{
						{Port: "8443", Address: "green-worker.example.com"},
					},
				},
			},
			expected: map[string]graphNodeSnapshot{
				"blue_api": {
					Endpoints: []string{"blue-api.example.com:443"},
				},
				"blue_gateway": {
					Depends: []string{"blue_api", "green_worker"},
				},
				"green_api": {},
				"green_worker": {
					Endpoints: []string{"green-worker.example.com:8443"},
				},
			},
		},
		{
			name: "ignores self references unknown services and duplicate dependencies",
			services: []service.Info{
				{
					Stack: "prod",
					Name:  "api",
					Environment: map[string]string{
						"SELF_HOST":      "api",
						"SELF_URL":       "http://prod_api:8080",
						"CACHE_ADDR":     "redis:6379",
						"CACHE_ENDPOINT": "http://redis/health",
						"UNKNOWN_HOST":   "missing",
					},
				},
				{
					Stack: "prod",
					Name:  "redis",
					WebRoutes: []webroute.Route{
						{Port: "6379", Address: "redis-admin.example.com"},
					},
				},
			},
			expected: map[string]graphNodeSnapshot{
				"prod_api": {
					Depends: []string{"prod_redis"},
				},
				"prod_redis": {
					Endpoints: []string{"redis-admin.example.com:6379"},
				},
			},
		},
		{
			name: "resolves dependencies from another stack by unique service name and dotted host",
			services: []service.Info{
				{
					Stack: "app",
					Name:  "api",
					Environment: map[string]string{
						"QDRANT_ADDR":   "qdrant:6333",
						"WORKER_URL":    "http://jobs.worker:8080/run",
						"SEARCH_HOST":   "tasks.search",
						"IGNORED_OTHER": "db",
					},
				},
				{Stack: "vector", Name: "qdrant"},
				{Stack: "jobs", Name: "worker"},
				{Stack: "search", Name: "search"},
				{Stack: "app", Name: "db"},
			},
			expected: map[string]graphNodeSnapshot{
				"app_api": {
					Depends: []string{"jobs_worker", "search_search", "vector_qdrant"},
				},
				"app_db":        {},
				"jobs_worker":   {},
				"search_search": {},
				"vector_qdrant": {},
			},
		},
		{
			name: "ignores ambiguous plain service names from another stack",
			services: []service.Info{
				{
					Stack: "app",
					Name:  "api",
					Environment: map[string]string{
						"CACHE_HOST": "redis",
					},
				},
				{Stack: "blue", Name: "redis"},
				{Stack: "green", Name: "redis"},
			},
			expected: map[string]graphNodeSnapshot{
				"app_api":     {},
				"blue_redis":  {},
				"green_redis": {},
			},
		},
	}

	builder := NewBuilder()

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			built := builder.Build(testCase.services)

			assert.Equal(t, testCase.expected, graphDependenciesByNodeName(built))
		})
	}
}

type graphNodeSnapshot struct {
	Endpoints []string
	Depends   []string
}

func graphDependenciesByNodeName(graph Graph) map[string]graphNodeSnapshot {
	nodes := make(map[string]graphNodeSnapshot, len(graph.Nodes))
	for _, node := range graph.Nodes {
		var dependencies []string
		if len(node.Depends) > 0 {
			dependencies = make([]string, 0, len(node.Depends))
			for _, dependency := range node.Depends {
				dependencies = append(dependencies, dependency.Name)
			}
		}

		nodes[node.Name] = graphNodeSnapshot{
			Endpoints: node.Endpoints,
			Depends:   dependencies,
		}
	}

	return nodes
}
