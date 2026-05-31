package drift

import (
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Analyzer compares desired compose service state with cluster runtime state.
type Analyzer struct {
	comparator Comparator
}

// NewAnalyzer creates drift analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		comparator: NewComposeComparator(),
	}
}

type Stack struct {
	Name string

	Desired compose.Compose
	Live    []*swarm.StackService
}

// Analyze detects drift for one service in a stack.
func (a *Analyzer) Analyze(stack Stack) (Response, error) {
	resp := Response{
		Drifts: []*Drift{},
	}

	liveServicesMap := make(map[string]*swarm.StackService)
	for _, service := range stack.Live {
		liveServicesMap[service.Name] = service
	}

	for _, service := range stack.Desired.Services {
		drift := &Drift{}

		live, serviceExists := liveServicesMap[service.Name]
		if !serviceExists {
			drift.ServiceMissed = true
			drift.OutOfSync = true
		} else {
			err := a.comparator.Compare(service, live.ServiceSpec, drift)
			if err != nil {
				return Response{}, fmt.Errorf("compare desired with live state for service %q: %w", service.Name, err)
			}
		}

		resp.Drifts = append(resp.Drifts, drift)
	}

	return resp, nil
}
