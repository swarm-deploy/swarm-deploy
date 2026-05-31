package srvcomparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

func TestServiceNetworkComparatorCompareNetworks(t *testing.T) {
	comparator := &NetworkComparator{}

	testCases := []struct {
		name      string
		left      *compose.ServiceNetworks
		right     *compose.ServiceNetworks
		expecteds []diff.NetworkDiff
	}{
		{
			name: "detects connected and disconnected networks",
			left: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "backend"},
				&compose.ServiceNetwork{Alias: "shared"},
			),
			right: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "shared"},
				&compose.ServiceNetwork{Alias: "frontend"},
			),
			expecteds: []diff.NetworkDiff{
				{Name: "backend", Connected: false},
				{Name: "frontend", Connected: true},
			},
		},
		{
			name: "returns empty for equal networks",
			left: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "backend"},
				&compose.ServiceNetwork{Alias: "frontend"},
			),
			right: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "backend"},
				&compose.ServiceNetwork{Alias: "frontend"},
			),
			expecteds: []diff.NetworkDiff{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			diffs := comparator.CompareNetworks(testCase.left, testCase.right)

			assert.Equal(t, testCase.expecteds, diffs, "unexpected network changes")
		})
	}
}

func TestServiceNetworkComparatorCompareSetsNetworkDiff(t *testing.T) {
	comparator := &NetworkComparator{}

	leftService := compose.Service{
		Networks: compose.NewServiceNetworks(
			&compose.ServiceNetwork{Alias: "backend"},
		),
	}
	rightService := compose.Service{
		Networks: compose.NewServiceNetworks(
			&compose.ServiceNetwork{Alias: "frontend"},
		),
	}

	serviceDiff := &diff.ServiceDiff{
		Networks: []diff.NetworkDiff{
			{Name: "old-network", Connected: true},
		},
	}

	comparator.Compare(leftService, rightService, serviceDiff)

	assert.Equal(
		t,
		[]diff.NetworkDiff{
			{Name: "backend", Connected: false},
			{Name: "frontend", Connected: true},
		},
		serviceDiff.Networks,
		"compare must write network diff to service diff",
	)
}
