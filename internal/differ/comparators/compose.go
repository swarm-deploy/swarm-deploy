package comparators

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type ComposeServiceComparator struct {
	comparators []ServiceComparator
}

func NewComposeServiceComparator(comparators ...ServiceComparator) ServiceComparator {
	return &ComposeServiceComparator{
		comparators: comparators,
	}
}

func (c *ComposeServiceComparator) Compare(left, right compose.Service, diff *diff.ServiceDiff) {
	for _, comparator := range c.comparators {
		comparator.Compare(left, right, diff)
	}
}
