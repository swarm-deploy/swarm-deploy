package analyzer

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

const (
	dockerAuthConfigEnvironment = "DOCKER_AUTH_CONFIG"
	dockerConfigEnvironment     = "DOCKER_CONFIG"
	dockerConfigFileName        = "config.json"
	defaultDockerConfigDir      = "/root/.docker"
	dockerSecretsDir            = "/run/secrets"
)

// SwarmDeployDockerConfigAnalyzer recommends exposing Docker registry credentials to swarm-deploy.
type SwarmDeployDockerConfigAnalyzer struct {
	now func() time.Time
}

// NewSwarmDeployDockerConfigAnalyzer creates a swarm-deploy Docker config recommendation analyzer.
func NewSwarmDeployDockerConfigAnalyzer() *SwarmDeployDockerConfigAnalyzer {
	return &SwarmDeployDockerConfigAnalyzer{now: time.Now}
}

// Analyze checks swarm-deploy services for Docker registry authentication configuration.
func (a *SwarmDeployDockerConfigAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Definition.Compose.Services {
		if !isSwarmDeployService(service) || hasDockerRegistryAuth(service) {
			continue
		}

		recs = append(recs, model.Recommendation{
			Severity: model.SeverityMedium,
			Type:     model.TypeServiceSwarmDeployDockerConfigMissing,
			Source:   model.SourceFromStack(stack),
			Subject: model.Subject{
				Stack:   stack.Name,
				Service: service.Name,
			},
			Title: "Docker registry credentials are not configured",
			Recommendation: "Provide Docker registry credentials to swarm-deploy using config.json. Prefer a Docker Secret " +
				"targeted as config.json with DOCKER_CONFIG=/run/secrets, or mount the file read-only at " +
				"$DOCKER_CONFIG/config.json, so authenticated registry images can be deployed",
			CreatedAt: a.now(),
		})
	}

	return recs
}

func isSwarmDeployService(service compose.Service) bool {
	image := strings.TrimSpace(service.Image)
	if image == "" {
		return strings.EqualFold(strings.TrimSpace(service.Name), "swarm-deploy")
	}

	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return strings.EqualFold(strings.TrimSpace(service.Name), "swarm-deploy")
	}

	switch reference.FamiliarName(named) {
	case "swarmdeployorg/swarm-deploy", "swarm-deploy":
		return true
	default:
		return strings.EqualFold(strings.TrimSpace(service.Name), "swarm-deploy")
	}
}

func hasDockerRegistryAuth(service compose.Service) bool {
	if strings.TrimSpace(service.Environment.Map[dockerAuthConfigEnvironment]) != "" {
		return true
	}

	configDir := strings.TrimSpace(service.Environment.Map[dockerConfigEnvironment])
	if configDir == "" {
		configDir = defaultDockerConfigDir
	}

	configDir = path.Clean(configDir)
	configPath := path.Join(configDir, dockerConfigFileName)

	for _, secret := range service.Secrets {
		if normalizeSecretTarget(secret) == configPath {
			return true
		}
	}

	for _, volume := range service.Volumes.Volumes {
		if volume == nil {
			continue
		}

		target := path.Clean(strings.TrimSpace(volume.Target))
		if target == configPath || target == configDir {
			return true
		}
	}

	return false
}

func normalizeSecretTarget(secret compose.ObjectRef) string {
	target := strings.TrimSpace(secret.Target)
	if target == "" {
		target = strings.TrimSpace(secret.Source)
	}
	if target == "" {
		return ""
	}
	if path.IsAbs(target) {
		return path.Clean(target)
	}

	return path.Join(dockerSecretsDir, target)
}
