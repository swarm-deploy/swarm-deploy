package srvcomparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestVolumeComparatorCompareVolumes(t *testing.T) {
	testCases := []struct {
		name     string
		left     compose.ServiceVolumes
		right    compose.ServiceVolumes
		expected []diff.VolumeDiff
	}{
		{
			name: "detects added removed and changed volumes",
			left: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{
				{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/data"},
				{Type: compose.ServiceVolumeTypeBind, Source: "./legacy", Target: "/legacy"},
			}},
			right: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{
				{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/data", ReadOnly: true},
				{Type: compose.ServiceVolumeTypeBind, Source: "./current", Target: "/current"},
			}},
			expected: []diff.VolumeDiff{
				{Type: "bind", Source: "./current", Target: "/current", Added: true},
				{Type: "volume", Source: "data", Target: "/data", ReadOnly: true, Added: true},
				{Type: "volume", Source: "data", Target: "/data", Removed: true},
				{Type: "bind", Source: "./legacy", Target: "/legacy", Removed: true},
			},
		},
		{
			name: "returns empty for equal volumes",
			left: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{
				{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/data"},
			}},
			right: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{
				{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/data"},
			}},
			expected: []diff.VolumeDiff{},
		},
	}

	comparator := &VolumeComparator{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := comparator.CompareVolumes(testCase.left, testCase.right)

			assert.Equal(t, testCase.expected, actual, "unexpected volume changes")
		})
	}
}
