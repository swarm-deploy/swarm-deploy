package drift

import (
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

	resp := &AnalyzeResponse{
		Drifts: make(map[string]ServiceDrift),
	}

	for _, desiredService := range req.Desired.Compose.Services {
		_, serviceExists := liveServiceMap[desiredService.Name]
		if !serviceExists {
			resp.Drifts[desiredService.Name] = ServiceDrift{
				ServiceName:   desiredService.Name,
				Reason:        "Service Missed",
				ServiceMissed: true,
			}
			continue
		}
	}

	return *resp, nil
}
