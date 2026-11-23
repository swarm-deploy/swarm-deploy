package swarm

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestNetworkManagerMapNetworkMapsFields(t *testing.T) {
	network := dockernetwork.Summary{
		Name:       "backend",
		Scope:      "swarm",
		Driver:     "overlay",
		Internal:   true,
		Attachable: true,
		Ingress:    false,
		Options: map[string]string{
			"encrypted": "true",
		},
		Labels: map[string]string{
			"com.example.team": "platform",
		},
	}

	mapped := (&networkManager{}).mapNetwork(network)

	assert.Equal(t, "backend", mapped.Name, "unexpected network name")
	assert.Equal(t, "swarm", mapped.Scope, "unexpected scope")
	assert.Equal(t, "overlay", mapped.Driver, "unexpected driver")
	assert.True(t, mapped.Internal, "unexpected internal flag")
	assert.True(t, mapped.Attachable, "unexpected attachable flag")
	assert.False(t, mapped.Ingress, "unexpected ingress flag")
	assert.Equal(t, map[string]string{"encrypted": "true"}, mapped.Options, "unexpected network options")
	assert.Equal(t, map[string]string{"com.example.team": "platform"}, mapped.Labels, "unexpected labels")
}

func TestBuildNetworkListFilters(t *testing.T) {
	tests := []struct {
		name           string
		filter         ListNetworksFilter
		expectedLabels []string
	}{
		{
			name: "all swarm networks",
		},
		{
			name:   "single stack uses Docker label filter",
			filter: ListNetworksFilter{Stacks: []string{"payments"}},
			expectedLabels: []string{
				stackNamespaceLabelKey + "=payments",
			},
		},
		{
			name: "multiple stacks are filtered after listing",
			filter: ListNetworksFilter{
				Stacks: []string{"payments", "platform"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filterArgs := buildNetworkListFilters(tt.filter)

			assert.Equal(t, []string{"swarm"}, filterArgs.Get("scope"), "unexpected scope filters")
			assert.ElementsMatch(t, tt.expectedLabels, filterArgs.Get("label"), "unexpected label filters")
		})
	}
}

func TestMatchesNetworkListFilter(t *testing.T) {
	tests := []struct {
		name     string
		stack    string
		filter   ListNetworksFilter
		expected bool
	}{
		{name: "empty filter", stack: "payments", expected: true},
		{name: "one matching stack", stack: "payments", filter: ListNetworksFilter{Stacks: []string{"payments"}}, expected: true},
		{name: "one different stack", stack: "platform", filter: ListNetworksFilter{Stacks: []string{"payments"}}, expected: false},
		{name: "one of multiple stacks", stack: "platform", filter: ListNetworksFilter{Stacks: []string{"payments", "platform"}}, expected: true},
		{name: "standalone network", filter: ListNetworksFilter{Stacks: []string{"payments", "platform"}}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, matchesNetworkListFilter(Network{Stack: tt.stack}, tt.filter))
		})
	}
}

func TestNetworkGetPreservesRequestedScope(t *testing.T) {
	for _, tc := range []struct {
		name, network, scope, wantID string
		traced                       bool
	}{
		{name: "unscoped host retains local identity", network: "host", wantID: "local-host-id"},
		{name: "scoped host resolves Swarm identity", network: "host", scope: "swarm", wantID: "swarm-host-id"},
		{name: "traced lookup forwards scope", network: "host", scope: "swarm", wantID: "swarm-host-id", traced: true},
		{name: "ordinary Swarm network keeps exact identity", network: "demo_backend", wantID: "overlay-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1.48/networks/"+tc.network, r.URL.Path)
				assert.Equal(t, tc.scope, r.URL.Query().Get("scope"))
				network := dockernetwork.Inspect{ID: "overlay-id", Name: tc.network, Scope: "swarm", Driver: "overlay"}
				if tc.network == "host" {
					network.ID, network.Scope, network.Driver = "local-host-id", "local", "host"
					if r.URL.Query().Get("scope") == "swarm" {
						network.ID, network.Scope = "swarm-host-id", "swarm"
					}
				}
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.MarshalWrite(w, network))
			}))
			defer server.Close()
			docker := newTestDockerClient(t, server)
			t.Cleanup(func() { assert.NoError(t, docker.Close()) })
			manager := newNetworkManager(docker)
			if tc.traced {
				manager = traceNetworkManager(trace.NewNoopTracerProvider().Tracer("test"), manager)
			}
			var options []dockernetwork.InspectOptions
			if tc.scope != "" {
				options = []dockernetwork.InspectOptions{{Scope: tc.scope}}
			}
			actual, err := manager.Get(t.Context(), tc.network, options...)
			require.NoError(t, err)
			assert.Equal(t, tc.wantID, actual.ID)
			assert.Equal(t, tc.network, actual.Name)
		})
	}
}
