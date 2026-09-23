package srvcomparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestInitJobComparatorCompare(t *testing.T) {
	testCases := []struct {
		name     string
		left     compose.Service
		right    compose.Service
		expected []diff.ServiceDiff
	}{
		{
			name: "detects added removed and changed init jobs",
			left: compose.Service{InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "migrate:1"},
				{Name: "remove", Image: "remove:1"},
			}},
			right: compose.Service{InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "migrate:2"},
				{Name: "add", Image: "add:1"},
			}},
			expected: []diff.ServiceDiff{
				{
					ServiceName: "add",
					Added:       true,
					HasChanges:  true,
					Image:       &diff.ImageDiff{New: "add:1"},
				},
				{
					ServiceName: "migrate",
					HasChanges:  true,
					Image:       &diff.ImageDiff{Old: "migrate:1", New: "migrate:2"},
				},
				{
					ServiceName: "remove",
					Removed:     true,
					HasChanges:  true,
					Image:       &diff.ImageDiff{Old: "remove:1"},
				},
			},
		},
		{
			name: "returns empty for equal init jobs",
			left: compose.Service{InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "migrate:1"},
			}},
			right: compose.Service{InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "migrate:1"},
			}},
			expected: []diff.ServiceDiff{},
		},
	}

	comparator := NewInitJobComparator([]Comparator{&ImageComparator{}})
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			serviceDiff := &diff.ServiceDiff{}

			comparator.Compare(testCase.left, testCase.right, serviceDiff)

			assert.Equal(t, testCase.expected, serviceDiff.InitJobs, "unexpected init job changes")
		})
	}
}
