package srvcomparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

func TestServiceEnvComparatorCompareEnv(t *testing.T) {
	comparator := &EnvComparator{}

	testCases := []struct {
		name      string
		leftEnv   []string
		rightEnv  []string
		expecteds []diff.EnvironmentDiff
	}{
		{
			name:     "detects added changed deleted",
			leftEnv:  []string{"A=1", "B=2", "D=4"},
			rightEnv: []string{"B=3", "C=5", "D=4"},
			expecteds: []diff.EnvironmentDiff{
				{VarName: "A", Value: "1", Deleted: true},
				{VarName: "B", Value: "3", Changed: true},
				{VarName: "C", Value: "5", Added: true},
			},
		},
		{
			name:      "returns empty for equal environment",
			leftEnv:   []string{"A=1", "B=2"},
			rightEnv:  []string{"A=1", "B=2"},
			expecteds: []diff.EnvironmentDiff{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			leftEnv := mustEnvironment(t, testCase.leftEnv...)
			rightEnv := mustEnvironment(t, testCase.rightEnv...)

			diffs := comparator.CompareEnv(leftEnv, rightEnv)

			assert.Equal(t, testCase.expecteds, diffs, "unexpected environment changes")
		})
	}
}

func TestServiceEnvComparatorCompareSetsEnvironmentDiff(t *testing.T) {
	comparator := &EnvComparator{}

	leftService := compose.Service{
		Environment: mustEnvironment(t, "A=1", "B=2"),
	}
	rightService := compose.Service{
		Environment: mustEnvironment(t, "A=1", "B=3"),
	}

	serviceDiff := &diff.ServiceDiff{
		Environment: []diff.EnvironmentDiff{
			{VarName: "OLD", Value: "should-be-overwritten", Added: true},
		},
	}

	comparator.Compare(leftService, rightService, serviceDiff)

	assert.Equal(
		t,
		[]diff.EnvironmentDiff{
			{VarName: "B", Value: "3", Changed: true},
		},
		serviceDiff.Environment,
		"compare must write environment diff to service diff",
	)
}

func mustEnvironment(t *testing.T, values ...string) compose.Environment {
	t.Helper()

	env, err := compose.NewEnvironment(values)
	require.NoError(t, err, "build test environment")

	return *env
}
