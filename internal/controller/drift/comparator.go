package drift

import (
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type Comparator interface {
	Compare(desired compose.Service, live dockerswarm.ServiceSpec, drift *Drift) error
}

type ComposeComparator struct {
	comparators []Comparator
}

func NewComposeComparator() Comparator {
	return &ComposeComparator{
		comparators: []Comparator{
			&EnvComparator{},
		},
	}
}

func (c *ComposeComparator) Compare(desired compose.Service, live dockerswarm.ServiceSpec, drift *Drift) error {
	for _, comparator := range c.comparators {
		if err := comparator.Compare(desired, live, drift); err != nil {
			return err
		}
	}

	return nil
}
