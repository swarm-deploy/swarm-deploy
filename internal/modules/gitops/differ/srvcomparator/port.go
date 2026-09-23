package srvcomparator

import (
	"reflect"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

type PortComparator struct{}

func (c *PortComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Ports = c.ComparePorts(left.Ports, right.Ports)
}

func (c *PortComparator) ComparePorts(leftPorts, rightPorts compose.ServicePorts) []diff.PortDiff {
	matchedRight := make([]bool, len(rightPorts.Ports))
	diffs := make([]diff.PortDiff, 0, len(leftPorts.Ports)+len(rightPorts.Ports))
	for _, leftPort := range leftPorts.Ports {
		match := findPort(leftPort, rightPorts.Ports, matchedRight)
		if match >= 0 {
			matchedRight[match] = true
			continue
		}
		diffs = append(diffs, portDiff(leftPort, false))
	}
	for i, rightPort := range rightPorts.Ports {
		if !matchedRight[i] {
			diffs = append(diffs, portDiff(rightPort, true))
		}
	}

	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Published == diffs[j].Published {
			if diffs[i].Target == diffs[j].Target {
				if diffs[i].Added != diffs[j].Added {
					return boolScore(diffs[i].Added) > boolScore(diffs[j].Added)
				}
				return stableJSON(diffs[i]) < stableJSON(diffs[j])
			}
			return diffs[i].Target < diffs[j].Target
		}
		return diffs[i].Published < diffs[j].Published
	})

	return diffs
}

func findPort(port compose.ServicePort, candidates []compose.ServicePort, matched []bool) int {
	for i, candidate := range candidates {
		if !matched[i] && portsEqual(port, candidate) {
			return i
		}
	}
	return -1
}

func portsEqual(left, right compose.ServicePort) bool {
	return left.Published == right.Published &&
		left.Target == right.Target &&
		left.Protocol == right.Protocol &&
		left.AppProtocol == right.AppProtocol &&
		left.Mode == right.Mode &&
		left.HostIP == right.HostIP &&
		reflect.DeepEqual(left.Extra, right.Extra)
}

func portDiff(port compose.ServicePort, added bool) diff.PortDiff {
	return diff.PortDiff{
		Published:   port.Published,
		Target:      port.Target,
		Protocol:    string(port.Protocol),
		AppProtocol: port.AppProtocol,
		Mode:        string(port.Mode),
		HostIP:      port.HostIP,
		Added:       added,
		Removed:     !added,
	}
}
