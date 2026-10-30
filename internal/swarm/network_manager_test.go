package swarm

import (
	"testing"

	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
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
