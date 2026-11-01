package drift

import (
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type Analyzer struct{}

func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

func (a *Analyzer) Analyze(req AnalyzeRequest) (AnalyzeResponse, error) {
	liveServiceMap := make(map[string]swarm.StackService)
	for _, service := range req.Live {
		liveServiceMap[service.Name] = service
	}

	resp := AnalyzeResponse{
		Drifts: make(map[string]ServiceDrift),
	}

	for _, desiredService := range req.Desired.Compose.Services {
		replicasEnabled, specified, err := desiredService.ReplicaDriftPolicy()
		if err != nil {
			return AnalyzeResponse{}, fmt.Errorf("resolve service %s replica drift policy: %w", desiredService.Name, err)
		}

		liveService, serviceExists := liveServiceMap[desiredService.Name]
		if !serviceExists {
			resp.Drifts[desiredService.Name] = ServiceDrift{
				ServiceName:   desiredService.Name,
				Reason:        "service is missing",
				ServiceMissed: true,
			}
			continue
		}

		if (specified && !replicasEnabled) || desiredService.Deploy.Replicas == nil {
			continue
		}

		var liveReplicas uint64
		if liveService.Replicas != nil {
			liveReplicas = *liveService.Replicas
		}
		if *desiredService.Deploy.Replicas == liveReplicas {
			continue
		}

		resp.Drifts[desiredService.Name] = ServiceDrift{
			ServiceName: desiredService.Name,
			Reason: fmt.Sprintf(
				"replica count differs: desired=%d actual=%d", *desiredService.Deploy.Replicas, liveReplicas,
			),
			Replicas: ReplicasDrift{
				OutOfSync: true,
				Desired:   *desiredService.Deploy.Replicas,
				Live:      liveReplicas,
			},
		}
	}

	return resp, nil
}
