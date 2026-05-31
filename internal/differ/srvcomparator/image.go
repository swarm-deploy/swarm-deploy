package srvcomparator

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type ImageComparator struct{}

func (s ImageComparator) Compare(left, right compose.Service, sdiff *diff.ServiceDiff) {
	if left.Image == right.Image {
		return
	}

	sdiff.Image = &diff.ImageDiff{
		Old: left.Image,
		New: right.Image,
	}
}
