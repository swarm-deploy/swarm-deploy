package srvcomparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestFieldComparatorsLifecycle(t *testing.T) {
	testCases := []struct {
		name       string
		comparator Comparator
		service    func(string) compose.Service
		changed    func(diff.ServiceDiff) bool
	}{
		{
			name:       "command",
			comparator: &CommandComparator{},
			service:    func(value string) compose.Service { return compose.Service{Command: command(value)} },
			changed:    func(result diff.ServiceDiff) bool { return result.Command != nil },
		},
		{
			name:       "healthcheck",
			comparator: &HealthcheckComparator{},
			service: func(value string) compose.Service {
				if value == "" {
					return compose.Service{}
				}
				return compose.Service{Healthcheck: &compose.ServiceHealth{Interval: value}}
			},
			changed: func(result diff.ServiceDiff) bool { return result.Healthcheck != nil },
		},
		{
			name:       "deploy",
			comparator: &DeployComparator{},
			service:    func(value string) compose.Service { return compose.Service{Deploy: compose.ServiceDeploy{Mode: value}} },
			changed:    func(result diff.ServiceDiff) bool { return result.Deploy != nil },
		},
		{
			name:       "capabilities",
			comparator: &CapabilitiesComparator{},
			service: func(value string) compose.Service {
				if value == "" {
					return compose.Service{}
				}
				return compose.Service{CapAdd: []string{value}, CapDrop: []string{"DROP_" + value}}
			},
			changed: func(result diff.ServiceDiff) bool { return result.CapAdd != nil && result.CapDrop != nil },
		},
		{
			name:       "labels",
			comparator: &LabelsComparator{},
			service: func(value string) compose.Service {
				if value == "" {
					return compose.Service{}
				}
				return compose.Service{Labels: *compose.NewLabels(map[string]string{"role": value})}
			},
			changed: func(result diff.ServiceDiff) bool { return result.Labels != nil },
		},
		{
			name:       "logging",
			comparator: &LoggingComparator{},
			service: func(value string) compose.Service {
				return compose.Service{Logging: compose.ServiceLogging{Driver: value}}
			},
			changed: func(result diff.ServiceDiff) bool { return result.Logging != nil },
		},
		{
			name:       "env_file",
			comparator: &EnvFileComparator{},
			service: func(value string) compose.Service {
				if value == "" {
					return compose.Service{}
				}
				return compose.Service{EnvFiles: []string{value}}
			},
			changed: func(result diff.ServiceDiff) bool { return result.EnvFiles != nil },
		},
	}

	lifecycleCases := []struct {
		name         string
		left         string
		right        string
		expectChange bool
	}{
		{name: "added", right: "new", expectChange: true},
		{name: "removed", left: "old", expectChange: true},
		{name: "changed", left: "old", right: "new", expectChange: true},
		{name: "unchanged", left: "same", right: "same"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, lifecycleCase := range lifecycleCases {
				t.Run(lifecycleCase.name, func(t *testing.T) {
					result := diff.ServiceDiff{}
					testCase.comparator.Compare(
						testCase.service(lifecycleCase.left),
						testCase.service(lifecycleCase.right),
						&result,
					)

					assert.Equal(t, lifecycleCase.expectChange, testCase.changed(result), "unexpected comparator result")
				})
			}
		})
	}
}

func TestCapabilitiesComparatorIgnoresOrder(t *testing.T) {
	result := diff.ServiceDiff{}
	comparator := &CapabilitiesComparator{}

	comparator.Compare(
		compose.Service{CapAdd: []string{"SYS_ADMIN", "NET_ADMIN"}},
		compose.Service{CapAdd: []string{"NET_ADMIN", "SYS_ADMIN"}},
		&result,
	)

	assert.Nil(t, result.CapAdd, "capability order must not produce a semantic change")
}

func command(value string) compose.Command {
	if value == "" {
		return compose.Command{}
	}
	return compose.NewCommand([]string{value})
}
