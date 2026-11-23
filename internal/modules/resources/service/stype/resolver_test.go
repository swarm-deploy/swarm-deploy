package stype

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolverResolveCloudSecrets(t *testing.T) {
	t.Parallel()

	resolver := NewResolver()

	assert.Equal(
		t,
		SecretManager,
		resolver.Resolve("ghcr.io/swarm-deploy/cloud-secrets:v0.4.1", Labels{}),
	)
}
