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
			// Deprecated provider-specific labels remain supported for backward compatibility.
			{label: labelsdict.GitLabRepository, provider: "gitlab"}, //nolint:staticcheck
			{label: labelsdict.GitHubRepository, provider: "github"}, //nolint:staticcheck
			{label: labelsdict.BitbucketRepository, provider: "bitbucket"}, //nolint:staticcheck
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
			provider := strings.ToLower(strings.TrimSpace(scope[labelsdict.SourceProvider]))
			return rawValue, provider
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
