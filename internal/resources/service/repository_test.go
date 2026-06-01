package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveRepositoryURL(t *testing.T) {
	t.Run("uses label priority order", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelGitHubRepository: "org/example-github",
				labelGitLabRepository: "org/example-gitlab",
			},
		}

		resolved := ResolveRepositoryURL(labels)

		assert.Equal(t, "org/example-gitlab", resolved, "unexpected repository URL")
	})

	t.Run("uses scope priority for same label key", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelGitHubRepository: "service/repo",
			},
			Container: map[string]string{
				labelGitHubRepository: "container/repo",
			},
			Image: map[string]string{
				labelGitHubRepository: "image/repo",
			},
		}

		resolved := ResolveRepositoryURL(labels)

		assert.Equal(t, "service/repo", resolved, "unexpected repository URL")
	})

	t.Run("returns provider value as-is", func(t *testing.T) {
		labels := Labels{
			Service: map[string]string{
				labelBitbucketRepository: "bitbucket.org/team/repo",
			},
		}

		resolved := ResolveRepositoryURL(labels)

		assert.Equal(t, "bitbucket.org/team/repo", resolved, "unexpected repository URL")
	})

	t.Run("uses oci source as fallback", func(t *testing.T) {
		labels := Labels{
			Image: map[string]string{
				labelOCIImageSource: "github.com/swarmdeployorg/swarm-deploy",
			},
		}

		resolved := ResolveRepositoryURL(labels)

		assert.Equal(t, "github.com/swarmdeployorg/swarm-deploy", resolved, "unexpected repository URL")
	})

	t.Run("ignores git ssh format", func(t *testing.T) {
		labels := Labels{
			Image: map[string]string{
				labelOCIImageSource: "git@github.com:swarmdeployorg/swarm-deploy.git",
			},
		}

		resolved := ResolveRepositoryURL(labels)

		assert.Equal(t, "", resolved, "unexpected repository URL")
	})

	t.Run("ignores ssh scheme url", func(t *testing.T) {
		labels := Labels{
			Image: map[string]string{
				labelOCIImageSource: "ssh://git@github.com/swarmdeployorg/swarm-deploy.git",
			},
		}

		resolved := ResolveRepositoryURL(labels)

		assert.Equal(t, "", resolved, "unexpected repository URL")
	})

	t.Run("returns empty when no labels found", func(t *testing.T) {
		resolved := ResolveRepositoryURL(Labels{})

		assert.Equal(t, "", resolved, "unexpected repository URL")
	})
}
