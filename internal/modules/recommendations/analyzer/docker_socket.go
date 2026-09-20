package analyzer

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

var dockerSocketPaths = map[string]struct{}{
	"/run/docker.sock":     {},
	"/var/run/docker.sock": {},
}

// DockerSocketMountAnalyzer detects services with access to the Docker daemon socket.
type DockerSocketMountAnalyzer struct {
	now func() time.Time
}

// NewDockerSocketMountAnalyzer creates a Docker socket mount recommendation analyzer.
func NewDockerSocketMountAnalyzer() *DockerSocketMountAnalyzer {
	return &DockerSocketMountAnalyzer{now: time.Now}
}

// Analyze inspects parsed Compose bind mounts for Docker daemon sockets.
func (a *DockerSocketMountAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Definition.Compose.Services {
		if !mountsDockerSocket(service) {
			continue
		}

		recs = append(recs, model.Recommendation{
			Severity: model.SeverityHigh,
			Type:     model.TypeServiceDockerSocketMount,
			Source:   model.SourceFromStack(stack),
			Subject: model.Subject{
				Stack:   stack.Name,
				Service: service.Name,
			},
			Title: "Docker socket is mounted",
			Recommendation: "Avoid mounting the Docker socket unless strictly required because it grants broad " +
				"access to the Docker daemon",
			CreatedAt: a.now(),
		})
	}

	return recs
}

func mountsDockerSocket(service compose.Service) bool {
	for _, volume := range service.Volumes.Volumes {
		if volume == nil || volume.Type != compose.ServiceVolumeTypeBind {
			continue
		}

		source := path.Clean(strings.TrimSpace(volume.Source))
		if _, ok := dockerSocketPaths[source]; ok {
			return true
		}
	}

	return false
}
