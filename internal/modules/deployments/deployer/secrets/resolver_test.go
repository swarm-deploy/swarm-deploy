package secrets

import (
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestSecretResolverDecodesLoadedBytes(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	value := []byte("resolver-private-marker")
	resolver := NewSecretResolver(NewAgeDecoder([]byte(identity.String())), PlaintextDecoder{})
	data, err := resolver.Resolve("app", compose.Secrets{
		"encrypted": {File: "./password.age", Data: encryptTestSecret(t, identity.Recipient(), value, false)},
		"plain":     {File: "/nonexistent/plain.txt", Data: value},
		"external":  {External: true, File: "missing.age"},
		"driver":    {Driver: "vault"},
	})
	require.NoError(t, err)
	defer Clear(data)
	assert.Equal(t, map[string][]byte{"encrypted": value, "plain": value}, data)
}

func TestSecretResolverDecoderSelection(t *testing.T) {
	resolver := NewSecretResolver(NewAgeDecoder(nil))
	_, err := resolver.Resolve("app", compose.Secrets{
		"plain": {File: "plain.txt", Data: []byte("ordinary")},
	})
	require.ErrorContains(t, err, "no decoder for secret file")
}
