package metadata

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

type repositoryLabelSource struct {
	key string
}

var repositoryLabelSources = []repositoryLabelSource{
	{key: labelsdict.GitLabRepository},
	{key: labelsdict.GitHubRepository},
	{key: labelsdict.BitbucketRepository},
	{key: labelsdict.OCIImageSource},
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
