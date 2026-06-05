package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
)

func TestBuilderBuild(t *testing.T) {
	tests := []struct {
		name     string
		services []service.Info
		expected map[string][]string
	}{
		{
			name: "builds dependencies from supported env suffixes",
			services: []service.Info{
				{
					Stack: "prod",
					Name:  "api",
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
			expected: map[string][]string{
				"prod_api":      {"prod_auth", "prod_db", "prod_payments", "prod_redis", "prod_search"},
				"prod_auth":     nil,
				"prod_db":       nil,
				"prod_payments": nil,
				"prod_redis":    nil,
				"prod_search":   nil,
				"prod_worker":   nil,
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
				{Stack: "blue", Name: "api"},
				{Stack: "green", Name: "api"},
				{Stack: "green", Name: "worker"},
			},
			expected: map[string][]string{
				"blue_api":     nil,
				"blue_gateway": {"blue_api", "green_worker"},
				"green_api":    nil,
				"green_worker": nil,
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
				{Stack: "prod", Name: "redis"},
			},
			expected: map[string][]string{
				"prod_api":   {"prod_redis"},
				"prod_redis": nil,
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

func graphDependenciesByNodeName(graph Graph) map[string][]string {
	nodes := make(map[string][]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if len(node.Depends) == 0 {
			nodes[node.Name] = nil
			continue
		}

		dependencies := make([]string, 0, len(node.Depends))
		for _, dependency := range node.Depends {
			dependencies = append(dependencies, dependency.Name)
		}

		nodes[node.Name] = dependencies
	}

	return nodes
}
