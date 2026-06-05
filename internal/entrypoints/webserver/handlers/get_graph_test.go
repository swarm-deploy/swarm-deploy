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
					},
					{
						Name: "redis",
						WebRoutes: []webroute.Route{
							{Port: "6379", Address: "redis.internal"},
						},
					},
				},
			},
			expected: map[string]graphResponseNodeSnapshot{
				"payments_api": {
					Kind:      generated.GraphNodeKindService,
					Endpoints: []string{"443:api.example.com"},
					Depends:   []string{"payments_db", "payments_redis"},
				},
				"payments_db": {
					Kind: generated.GraphNodeKindService,
				},
				"payments_redis": {
					Kind:      generated.GraphNodeKindService,
					Endpoints: []string{"6379:redis.internal"},
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
		var dependencies []string
		if len(node.Depends) > 0 {
			dependencies = make([]string, 0, len(node.Depends))
			for _, dependency := range node.Depends {
				dependencies = append(dependencies, dependency.Name)
			}
		}

		var endpoints []string
		if len(node.Endpoints) > 0 {
			endpoints = append(endpoints, node.Endpoints...)
		}

		snapshots[node.Name] = graphResponseNodeSnapshot{
			Kind:      node.Kind,
			Endpoints: endpoints,
			Depends:   dependencies,
		}
	}

	return snapshots
}
