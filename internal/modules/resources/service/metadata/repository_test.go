package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestResolveRepository(t *testing.T) {
	extractor := NewRepositoryResolver()

	t.Run("uses generic repository and provider", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.SourceRepository: "https://github.com/acme/api",
				labelsdict.SourceProvider:   "github",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Equal(t, "https://github.com/acme/api", url)
		assert.Equal(t, "github", provider)
	})

	t.Run("generic labels take precedence over deprecated labels", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.SourceRepository: "https://example.com/acme/api",
				labelsdict.SourceProvider:   "gitea",
				labelsdict.GitHubRepository: "https://github.com/acme/api",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Equal(t, "https://example.com/acme/api", url)
		assert.Equal(t, "gitea", provider)
	})

	t.Run("uses scope priority for generic labels", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.SourceRepository: "https://service.example/repo",
				labelsdict.SourceProvider:   "service-provider",
			},
			Container: map[string]string{
				labelsdict.SourceRepository: "https://container.example/repo",
				labelsdict.SourceProvider:   "container-provider",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Equal(t, "https://service.example/repo", url)
		assert.Equal(t, "service-provider", provider)
	})

	t.Run("supports generic repository without provider", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.SourceRepository: "https://example.com/acme/api",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Equal(t, "https://example.com/acme/api", url)
		assert.Empty(t, provider)
	})

	t.Run("supports deprecated provider labels and infers provider", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.GitHubRepository: "https://github.com/acme/api",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Equal(t, "https://github.com/acme/api", url)
		assert.Equal(t, "github", provider)
	})

	t.Run("keeps legacy label priority", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.GitHubRepository: "org/example-github",
				labelsdict.GitLabRepository: "org/example-gitlab",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Equal(t, "org/example-gitlab", url)
		assert.Equal(t, "gitlab", provider)
	})

	t.Run("uses oci source as fallback", func(t *testing.T) {
		labels := Labels{
			Image: map[string]string{
				labelsdict.OCIImageSource: "github.com/swarmdeployorg/swarm-deploy",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Equal(t, "github.com/swarmdeployorg/swarm-deploy", url)
		assert.Empty(t, provider)
	})

	t.Run("ignores git ssh format", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.SourceRepository: "git@github.com:swarmdeployorg/swarm-deploy.git",
				labelsdict.SourceProvider:   "github",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Empty(t, url)
		assert.Empty(t, provider)
	})

	t.Run("ignores ssh scheme url", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.SourceRepository: "ssh://git@github.com/swarmdeployorg/swarm-deploy.git",
				labelsdict.SourceProvider:   "github",
			},
		}

		url, provider := extractor.resolve(labels)

		assert.Empty(t, url)
		assert.Empty(t, provider)
	})

	t.Run("returns empty when no labels found", func(t *testing.T) {
		url, provider := extractor.resolve(Labels{})

		assert.Empty(t, url)
		assert.Empty(t, provider)
	})
}
