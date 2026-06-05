package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	resourcegraph "github.com/swarm-deploy/swarm-deploy/internal/resources/graph"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/resources/service/stype"
	"github.com/swarm-deploy/webroute"
)

func TestGetDependencyGraphExecute(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		services []service.Info
		expected map[string]graphNodeSnapshot
	}{
		{
			name: "builds dependency graph from stored services",
			services: []service.Info{
				{
					Name:  "api",
					Stack: "payments",
					Type:  serviceType.Application,
					WebRoutes: []webroute.Route{
						{Port: "443", Address: "api.example.com"},
					},
					Environment: map[string]string{
						"DB_HOST":   "db",
						"REDIS_URL": "redis:6379",
					},
				},
				{
					Name:  "db",
					Stack: "payments",
					Type:  serviceType.Database,
				},
				{
					Name:  "redis",
					Stack: "payments",
					Type:  serviceType.Monitoring,
					WebRoutes: []webroute.Route{
						{Port: "6379", Address: "redis.internal"},
					},
				},
			},
			expected: map[string]graphNodeSnapshot{
				"payments_api": {
					Kind:      resourcegraph.KindApplication,
					Endpoints: []string{"api.example.com:443"},
					Depends:   []string{"payments_db", "payments_redis"},
				},
				"payments_db": {
					Kind: resourcegraph.KindDatabase,
				},
				"payments_redis": {
					Kind:      resourcegraph.KindMonitoring,
					Endpoints: []string{"redis.internal:6379"},
				},
			},
		},
		{
			name:     "returns empty graph for empty store",
			services: nil,
			expected: map[string]graphNodeSnapshot{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tool := NewGetDependencyGraph(&fakeServiceStore{
				services: testCase.services,
			})

			response, err := tool.Execute(context.Background(), routing.Request{})
			require.NoError(t, err, "execute dependency_graph_get tool")

			var payload struct {
				Nodes []resourcegraph.Node `json:"nodes"`
			}

			encoded, err := json.Marshal(response.Payload)
			require.NoError(t, err, "encode response payload")
			require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")

			assert.Equal(t, testCase.expected, graphNodesByName(payload.Nodes))
		})
	}
}

type graphNodeSnapshot struct {
	Kind      resourcegraph.Kind
	Endpoints []string
	Depends   []string
}

func graphNodesByName(nodes []resourcegraph.Node) map[string]graphNodeSnapshot {
	snapshots := make(map[string]graphNodeSnapshot, len(nodes))
	for _, node := range nodes {
		var endpoints []string
		if len(node.Endpoints) > 0 {
			endpoints = append(endpoints, node.Endpoints...)
		}

		snapshots[node.Name] = graphNodeSnapshot{
			Kind:      node.Kind,
			Endpoints: endpoints,
			Depends:   node.Depends,
		}
	}

	return snapshots
}
