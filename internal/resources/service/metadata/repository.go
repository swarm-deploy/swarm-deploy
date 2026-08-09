package metadata

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

type RepositoryResolver struct {
	sources []string
}

func NewRepositoryResolver() *RepositoryResolver {
	return &RepositoryResolver{
		sources: []string{
			labelsdict.GitLabRepository,
			labelsdict.GitHubRepository,
			labelsdict.BitbucketRepository,
			labelsdict.OCIImageSource,
		},
	}
}

func (r *RepositoryResolver) Resolve(labels Labels, meta *Metadata) {
	meta.RepositoryURL = r.resolveURL(labels)
}

// resolveURL resolves repository URL from labels by priority.
func (r *RepositoryResolver) resolveURL(labels Labels) string {
	labelScopes := []map[string]string{
		labels.Service,
		labels.Container,
		labels.Image,
	}

	for _, source := range r.sources {
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
