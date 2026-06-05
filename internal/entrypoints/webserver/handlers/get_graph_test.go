package handlers

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/resources/service/stype"
	"github.com/swarm-deploy/webroute"
)

func TestHandlerGetGraph(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		stacks   map[string][]service.Info
		expected map[string]graphResponseNodeSnapshot
	}{
		{
			name: "builds graph response from stored services",
			stacks: map[string][]service.Info{
				"payments": {
					{
						Name: "api",
						Type: serviceType.Application,
						WebRoutes: []webroute.Route{
							{Port: "443", Address: "api.example.com"},
						},
						Environment: map[string]string{
							"DB_HOST":   "db",
							"REDIS_URL": "redis:6379",
						},
					},
					{
						Name: "db",
						Type: serviceType.Database,
					},
					{
						Name: "redis",
						Type: serviceType.Monitoring,
						WebRoutes: []webroute.Route{
							{Port: "6379", Address: "redis.internal"},
						},
					},
				},
			},
			expected: map[string]graphResponseNodeSnapshot{
				"payments_api": {
					Kind:      generated.GraphNodeKindApplication,
					Endpoints: []string{"api.example.com:443"},
					Depends:   []string{"payments_db", "payments_redis"},
				},
				"payments_db": {
					Kind: generated.GraphNodeKindDatabase,
				},
				"payments_redis": {
					Kind:      generated.GraphNodeKindMonitoring,
					Endpoints: []string{"redis.internal:6379"},
				},
			},
		},
		{
			name:     "returns empty graph for empty store",
			stacks:   map[string][]service.Info{},
			expected: map[string]graphResponseNodeSnapshot{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store, err := service.NewStore(filepath.Join(t.TempDir(), "services.json"))
			require.NoError(t, err)

			stackNames := make([]string, 0, len(testCase.stacks))
			for stackName := range testCase.stacks {
				stackNames = append(stackNames, stackName)
			}
			sort.Strings(stackNames)

			for _, stackName := range stackNames {
				require.NoError(t, store.ReplaceStack(stackName, testCase.stacks[stackName]))
			}

			h := &handler{
				services: store,
			}

			resp, err := h.GetGraph(context.Background())
			require.NoError(t, err)
			require.NotNil(t, resp)

			assert.Equal(t, testCase.expected, graphResponseNodesByName(resp.Nodes))
		})
	}
}

type graphResponseNodeSnapshot struct {
	Kind      generated.GraphNodeKind
	Endpoints []string
	Depends   []string
}

func graphResponseNodesByName(nodes []generated.GraphNode) map[string]graphResponseNodeSnapshot {
	snapshots := make(map[string]graphResponseNodeSnapshot, len(nodes))
	for _, node := range nodes {
		var endpoints []string
		if len(node.Endpoints) > 0 {
			endpoints = append(endpoints, node.Endpoints...)
		}

		snapshots[node.Name] = graphResponseNodeSnapshot{
			Kind:      node.Kind,
			Endpoints: endpoints,
			Depends:   node.Depends,
		}
	}

	return snapshots
}
