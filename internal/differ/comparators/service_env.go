package comparators

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type ServiceEnvComparator struct{}

func (c *ServiceEnvComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	diffs := c.CompareEnv(left.Environment, right.Environment)
	srvDiff.Environment = diffs
}

func (c *ServiceEnvComparator) CompareEnv(left compose.Environment, right compose.Environment) []diff.EnvironmentDiff {
	variableNames := left.Keys
	for _, variableName := range right.Keys {
		if !left.Has(variableName) {
			variableNames = append(variableNames, variableName)
		}
	}

	diffs := make([]diff.EnvironmentDiff, 0, len(variableNames))
	for _, variableName := range variableNames {
		oldValue, oldExists := left.Get(variableName)
		newValue, newExists := right.Get(variableName)

		switch {
		case !oldExists && newExists:
			diffs = append(diffs, diff.EnvironmentDiff{
				VarName: variableName,
				Value:   newValue,
				Added:   true,
			})
		case oldExists && !newExists:
			diffs = append(diffs, diff.EnvironmentDiff{
				VarName: variableName,
				Value:   oldValue,
				Deleted: true,
			})
		case oldExists && oldValue != newValue:
			diffs = append(diffs, diff.EnvironmentDiff{
				VarName: variableName,
				Value:   newValue,
				Changed: true,
			})
		}
	}

	return diffs
}
