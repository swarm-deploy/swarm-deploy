package comparators

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type ServiceImageComparator struct{}

func (s ServiceImageComparator) Compare(left, right compose.Service, sdiff *diff.ServiceDiff) {
	if left.Image == right.Image {
		return
	}

	sdiff.Image = &diff.ImageDiff{
		Old: left.Image,
		New: right.Image,
	}
}
