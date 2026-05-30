package serviceupdater

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRepositoryReferenceHTTPS(t *testing.T) {
	ref, err := parseRepositoryReference("https://github.com/acme/swarm-config.git")
	require.NoError(t, err, "parse repository reference")

	assert.Equal(t, "https", ref.webScheme, "unexpected scheme")
	assert.Equal(t, "github.com", ref.host, "unexpected host")
	assert.Equal(t, "acme", ref.owner, "unexpected owner")
	assert.Equal(t, "swarm-config", ref.name, "unexpected repository name")
}

func TestParseRepositoryReferenceSSH(t *testing.T) {
	ref, err := parseRepositoryReference("git@github.com:acme/swarm-config.git")
	require.NoError(t, err, "parse repository reference")

	assert.Equal(t, "https", ref.webScheme, "unexpected scheme")
	assert.Equal(t, "github.com", ref.host, "unexpected host")
	assert.Equal(t, "acme", ref.owner, "unexpected owner")
	assert.Equal(t, "swarm-config", ref.name, "unexpected repository name")
}

func TestBuildBranchURL(t *testing.T) {
	url, err := buildBranchURL("git@github.com:acme/swarm-config.git", "api-up-image-2.0.0")
	require.NoError(t, err, "build branch url")
	assert.Equal(t, "https://github.com/acme/swarm-config/tree/api-up-image-2.0.0", url, "unexpected branch url")
}
