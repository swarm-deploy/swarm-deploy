package srvcomparator

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type NetworkComparator struct{}

func (c *NetworkComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Networks = c.CompareNetworks(left.Networks, right.Networks)
}

func (c *NetworkComparator) CompareNetworks(
	leftNetworks *compose.ServiceNetworks,
	rightNetworks *compose.ServiceNetworks,
) []diff.NetworkDiff {
	networkNames := leftNetworks.GetAliases()
	for _, networkName := range rightNetworks.GetAliases() {
		if leftNetworks.HasAlias(networkName) {
			continue
		}
		networkNames = append(networkNames, networkName)
	}

	diffs := make([]diff.NetworkDiff, 0, len(networkNames))
	for _, networkName := range networkNames {
		leftExists := leftNetworks.HasAlias(networkName)
		rightExists := rightNetworks.HasAlias(networkName)

		switch {
		case !leftExists && rightExists:
			diffs = append(diffs, diff.NetworkDiff{Name: networkName, Connected: true})
		case leftExists && !rightExists:
			diffs = append(diffs, diff.NetworkDiff{Name: networkName, Connected: false})
		}
	}

	return diffs
}
