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

func TestHealthcheckComparatorReportsFieldChanges(t *testing.T) {
	result := diff.ServiceDiff{}
	(&HealthcheckComparator{}).Compare(
		compose.Service{Healthcheck: &compose.ServiceHealth{
			Test: compose.NewCommand([]string{"CMD", "old"}), Interval: "10s", Retries: ptr(uint64(2)),
		}},
		compose.Service{Healthcheck: &compose.ServiceHealth{
			Test: compose.NewCommand([]string{"CMD", "new"}), Interval: "20s", Retries: ptr(uint64(3)),
		}},
		&result,
	)

	assert.Equal(t, &diff.HealthcheckDiff{
		Test:     &diff.CommandDiff{Old: []string{"CMD", "old"}, New: []string{"CMD", "new"}},
		Interval: &diff.ScalarDiff[string]{Old: "10s", New: "20s"},
		Retries:  &diff.ScalarDiff[uint64]{Old: 2, New: 3},
	}, result.Healthcheck, "unexpected healthcheck diff")
}

func TestDeployComparatorReportsNestedChanges(t *testing.T) {
	result := diff.ServiceDiff{}
	(&DeployComparator{}).Compare(
		compose.Service{Deploy: compose.ServiceDeploy{
			Mode: "replicated", Replicas: ptr(uint64(2)),
			Placement: &compose.ServiceDeployPlacement{
				Constraints: []string{"node.role==worker", "node.labels.zone==a"},
				Preferences: []compose.ServiceDeployPlacementPreference{{Spread: "node.labels.zone"}},
			},
			Resources:    &compose.ServiceDeployResources{Limits: &compose.ServiceDeployResource{Cpus: "0.5"}},
			UpdateConfig: &compose.ServiceDeployUpdateConfig{Order: "stop-first"},
		}},
		compose.Service{Deploy: compose.ServiceDeploy{
			Mode: "global", Replicas: ptr(uint64(3)),
			Placement: &compose.ServiceDeployPlacement{
				Constraints: []string{"node.labels.arch==arm64", "node.role==worker"},
				Preferences: []compose.ServiceDeployPlacementPreference{{Spread: "node.labels.rack"}},
			},
			Resources:    &compose.ServiceDeployResources{Limits: &compose.ServiceDeployResource{Cpus: "1.0"}},
			UpdateConfig: &compose.ServiceDeployUpdateConfig{Order: "start-first"},
		}},
		&result,
	)

	assert.Equal(t, &diff.DeployDiff{
		Mode:     &diff.ScalarDiff[string]{Old: "replicated", New: "global"},
		Replicas: &diff.ScalarDiff[uint64]{Old: 2, New: 3},
		Placement: &diff.PlacementDiff{
			Constraints: &diff.StringSetDiff{
				Added: []string{"node.labels.arch==arm64"}, Removed: []string{"node.labels.zone==a"},
			},
			Preferences: &diff.StringListDiff{
				Old: []string{"node.labels.zone"}, New: []string{"node.labels.rack"},
			},
		},
		Resources: &diff.ResourcesDiff{Limits: &diff.ResourceValuesDiff{
			CPUs: &diff.ScalarDiff[string]{Old: "0.5", New: "1.0"},
		}},
		UpdateConfig: &diff.UpdateConfigDiff{
			Order: &diff.ScalarDiff[string]{Old: "stop-first", New: "start-first"},
		},
	}, result.Deploy, "unexpected nested deploy diff")
}

func TestCapabilitiesComparatorReportsSortedSetChanges(t *testing.T) {
	result := diff.ServiceDiff{}
	(&CapabilitiesComparator{}).Compare(
		compose.Service{CapAdd: []string{"SYS_TIME", "CHOWN", "NET_ADMIN"}},
		compose.Service{CapAdd: []string{"DAC_OVERRIDE", "CHOWN", "AUDIT_WRITE"}},
		&result,
	)

	assert.Equal(t, &diff.StringSetDiff{
		Added: []string{"AUDIT_WRITE", "DAC_OVERRIDE"}, Removed: []string{"NET_ADMIN", "SYS_TIME"},
	}, result.CapAdd, "capability changes must be sorted")
}

func TestLabelsComparatorReportsSortedPerKeyChanges(t *testing.T) {
	result := diff.ServiceDiff{}
	(&LabelsComparator{}).Compare(
		compose.Service{Labels: *compose.NewLabels(map[string]string{
			"z-removed": "old", "m-changed": "old", "same": "value",
		})},
		compose.Service{Labels: *compose.NewLabels(map[string]string{
			"a-added": "new", "m-changed": "new", "same": "value",
		})},
		&result,
	)

	assert.Equal(t, []diff.LabelDiff{
		{Key: "a-added", New: "new", Added: true},
		{Key: "m-changed", Old: "old", New: "new", Changed: true},
		{Key: "z-removed", Old: "old", Removed: true},
	}, result.Labels, "label changes must be semantic and sorted")
}

func TestLoggingComparatorReportsSortedOptionChanges(t *testing.T) {
	result := diff.ServiceDiff{}
	(&LoggingComparator{}).Compare(
		compose.Service{Logging: compose.ServiceLogging{
			Driver: "json-file", Options: map[string]string{"z-removed": "old", "m-changed": "old"},
		}},
		compose.Service{Logging: compose.ServiceLogging{
			Driver: "local", Options: map[string]string{"a-added": "new", "m-changed": "new"},
		}},
		&result,
	)

	assert.Equal(t, &diff.LoggingDiff{
		Driver: &diff.ScalarDiff[string]{Old: "json-file", New: "local"},
		Options: []diff.LoggingOptionDiff{
			{Key: "a-added", New: "new", Added: true},
			{Key: "m-changed", Old: "old", New: "new", Changed: true},
			{Key: "z-removed", Old: "old", Removed: true},
		},
	}, result.Logging, "logging changes must be semantic and sorted")
}

func TestEnvFileComparatorPreservesOrder(t *testing.T) {
	result := diff.ServiceDiff{}
	(&EnvFileComparator{}).Compare(
		compose.Service{EnvFiles: []string{"base.env", "override.env"}},
		compose.Service{EnvFiles: []string{"override.env", "base.env"}},
		&result,
	)

	assert.Equal(t, &diff.StringListDiff{
		Old: []string{"base.env", "override.env"}, New: []string{"override.env", "base.env"},
	}, result.EnvFiles, "env_file order changes must be retained")
}

func ptr[T any](value T) *T {
	return &value
}

func command(value string) compose.Command {
	if value == "" {
		return compose.Command{}
	}
	return compose.NewCommand([]string{value})
}
