package service

import (
	"strings"
)

const (
	labelGitLabRepository    = "org.swarm_deploy.gitlab_repository"
	labelGitHubRepository    = "org.swarm_deploy.github_repository"
	labelBitbucketRepository = "org.swarm_deploy.bitbucket_repository"
	labelOCIImageSource      = "org.opencontainers.image.source"
)

type repositoryLabelSource struct {
	key string
}

var repositoryLabelSources = []repositoryLabelSource{
	{key: labelGitLabRepository},
	{key: labelGitHubRepository},
	{key: labelBitbucketRepository},
	{key: labelOCIImageSource},
}

// ResolveRepositoryURL resolves repository URL from labels by priority.
func ResolveRepositoryURL(labels Labels) string {
	labelScopes := []map[string]string{
		labels.Service,
		labels.Container,
		labels.Image,
	}

	for _, source := range repositoryLabelSources {
		for _, scope := range labelScopes {
			if len(scope) == 0 {
				continue
			}

			rawValue, ok := scope[source.key]
			if !ok {
				continue
			}

			lowerValue := strings.ToLower(rawValue)
			if strings.HasPrefix(lowerValue, "ssh://") || strings.HasPrefix(rawValue, "git@") {
				continue
			}
			if rawValue != "" {
				return rawValue
			}
		}
	}

	return ""
}
