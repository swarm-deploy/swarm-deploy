package metadata

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

type repositorySource struct {
	label    string
	provider string
}

type RepositoryResolver struct {
	legacySources []repositorySource
}

func NewRepositoryResolver() *RepositoryResolver {
	return &RepositoryResolver{
		legacySources: []repositorySource{
			{label: labelsdict.GitLabRepository, provider: "gitlab"},
			{label: labelsdict.GitHubRepository, provider: "github"},
			{label: labelsdict.BitbucketRepository, provider: "bitbucket"},
			{label: labelsdict.OCIImageSource},
		},
	}
}

func (r *RepositoryResolver) Resolve(labels Labels, meta *Metadata) {
	meta.RepositoryURL, meta.RepositoryProvider = r.resolve(labels)
}

func (r *RepositoryResolver) resolve(labels Labels) (string, string) {
	labelScopes := []map[string]string{
		labels.Service,
		labels.Container,
		labels.Image,
	}

	for _, scope := range labelScopes {
		if len(scope) == 0 {
			continue
		}

		if rawValue := validRepositoryURL(scope[labelsdict.SourceRepository]); rawValue != "" {
			return rawValue, strings.TrimSpace(scope[labelsdict.SourceProvider])
		}
	}

	for _, source := range r.legacySources {
		for _, scope := range labelScopes {
			if len(scope) == 0 {
				continue
			}

			if rawValue := validRepositoryURL(scope[source.label]); rawValue != "" {
				return rawValue, source.provider
			}
		}
	}

	return "", ""
}

func validRepositoryURL(rawValue string) string {
	if rawValue == "" {
		return ""
	}

	lowerValue := strings.ToLower(rawValue)
	if strings.HasPrefix(lowerValue, "ssh://") || strings.HasPrefix(rawValue, "git@") {
		return ""
	}

	return rawValue
}
