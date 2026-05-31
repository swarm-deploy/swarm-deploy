package comparators

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type ServiceComparator interface {
	Compare(left, right compose.Service, diff *diff.ServiceDiff)
}
