package srvcomparator

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestConfigComparatorCompareConfigs(t *testing.T) {
	oldMode := os.FileMode(0o440)
	newMode := os.FileMode(0o400)

	testCases := []struct {
		name     string
		left     []compose.ObjectRef
		right    []compose.ObjectRef
		expected []diff.ConfigDiff
	}{
		{
			name: "detects added removed and changed configs",
			left: []compose.ObjectRef{
				{Source: "app", Target: "/etc/app.yaml", Mode: &oldMode},
				{Source: "legacy", Target: "/etc/legacy.yaml"},
			},
			right: []compose.ObjectRef{
				{Source: "app", Target: "/etc/app.yaml", Mode: &newMode},
				{Source: "current", Target: "/etc/current.yaml"},
			},
			expected: []diff.ConfigDiff{
				{Name: "app", MountFile: "/etc/app.yaml", Mode: uint32Ptr(0o400), Added: true},
				{Name: "app", MountFile: "/etc/app.yaml", Mode: uint32Ptr(0o440), Removed: true},
				{Name: "current", MountFile: "/etc/current.yaml", Added: true},
				{Name: "legacy", MountFile: "/etc/legacy.yaml", Removed: true},
			},
		},
		{
			name:     "returns empty for equal configs",
			left:     []compose.ObjectRef{{Source: "app", Target: "/etc/app.yaml"}},
			right:    []compose.ObjectRef{{Source: "app", Target: "/etc/app.yaml"}},
			expected: []diff.ConfigDiff{},
		},
	}

	comparator := &ConfigComparator{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := comparator.CompareConfigs(testCase.left, testCase.right)

			assert.Equal(t, testCase.expected, actual, "unexpected config changes")
		})
	}
}

func uint32Ptr(value uint32) *uint32 {
	return &value
}
