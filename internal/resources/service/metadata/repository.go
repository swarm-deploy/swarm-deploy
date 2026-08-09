package metadata

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

var repositoryLabelSources = []string{
	labelsdict.GitLabRepository,
	labelsdict.GitHubRepository,
	labelsdict.BitbucketRepository,
	labelsdict.OCIImageSource,
}

// resolveRepositoryURL resolves repository URL from labels by priority.
func (r *Extractor) resolveRepositoryURL(labels Labels) string {
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

			rawValue, ok := scope[source]
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
