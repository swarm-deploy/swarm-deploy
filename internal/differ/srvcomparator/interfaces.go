package srvcomparator

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type Comparator interface {
	Compare(left, right compose.Service, diff *diff.ServiceDiff)
}

type ComposeComparator struct {
	comparators []Comparator
}

func NewComposeComparator(comparators ...Comparator) Comparator {
	return &ComposeComparator{
		comparators: comparators,
	}
}

func (c *ComposeComparator) Compare(left, right compose.Service, diff *diff.ServiceDiff) {
	for _, comparator := range c.comparators {
		comparator.Compare(left, right, diff)
	}
}
