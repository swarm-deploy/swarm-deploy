package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestResolveRepositoryURL(t *testing.T) {
	extractor := NewExtractor()

	t.Run("uses label priority order", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.GitHubRepository: "org/example-github",
				labelsdict.GitLabRepository: "org/example-gitlab",
			},
		}

		resolved := extractor.resolveRepositoryURL(labels)

		assert.Equal(t, "org/example-gitlab", resolved, "unexpected repository URL")
	})

	t.Run("uses scope priority for same label key", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.GitHubRepository: "service/repo",
			},
			Container: map[string]string{
				labelsdict.GitHubRepository: "container/repo",
			},
			Image: map[string]string{
				labelsdict.GitHubRepository: "image/repo",
			},
		}

		resolved := extractor.resolveRepositoryURL(labels)

		assert.Equal(t, "service/repo", resolved, "unexpected repository URL")
	})

	t.Run("returns provider value as-is", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelsdict.BitbucketRepository: "bitbucket.org/team/repo",
			},
		}

		resolved := extractor.resolveRepositoryURL(labels)

		assert.Equal(t, "bitbucket.org/team/repo", resolved, "unexpected repository URL")
	})

	t.Run("uses oci source as fallback", func(t *testing.T) {
		labels := Labels{
			Image: map[string]string{
				labelsdict.OCIImageSource: "github.com/swarmdeployorg/swarm-deploy",
			},
		}

		resolved := extractor.resolveRepositoryURL(labels)

		assert.Equal(t, "github.com/swarmdeployorg/swarm-deploy", resolved, "unexpected repository URL")
	})

	t.Run("ignores git ssh format", func(t *testing.T) {
		labels := Labels{
			Image: map[string]string{
				labelsdict.OCIImageSource: "git@github.com:swarmdeployorg/swarm-deploy.git",
			},
		}

		resolved := extractor.resolveRepositoryURL(labels)

		assert.Equal(t, "", resolved, "unexpected repository URL")
	})

	t.Run("ignores ssh scheme url", func(t *testing.T) {
		labels := Labels{
			Image: map[string]string{
				labelsdict.OCIImageSource: "ssh://git@github.com/swarmdeployorg/swarm-deploy.git",
			},
		}

		resolved := extractor.resolveRepositoryURL(labels)

		assert.Equal(t, "", resolved, "unexpected repository URL")
	})

	t.Run("returns empty when no labels found", func(t *testing.T) {
		resolved := extractor.resolveRepositoryURL(Labels{})

		assert.Equal(t, "", resolved, "unexpected repository URL")
	})
}
