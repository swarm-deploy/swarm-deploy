package secrets

import (
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestSecretResolverDecodesLoadedContent(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	value := []byte("resolver-private-marker")
	// The directory does not exist: the resolver must never read the repository working tree.
	sourceComposePath := filepath.Join(t.TempDir(), "missing", "compose.yaml")
	resolver := NewSecretResolver(NewAgeDecoder([]byte(identity.String())), PlaintextDecoder{})

	data, err := resolver.Resolve("app", sourceComposePath, compose.Secrets{
		"encrypted": {File: "./password.age", Data: encryptTestSecret(t, identity.Recipient(), value, false)},
		"plain":     {File: "/run/plain.txt", Data: value},
		"external":  {External: true, File: "missing.age"},
		"driver":    {Driver: "vault"},
	})
	require.NoError(t, err)
	defer Clear(data)
	assert.Equal(t, map[string][]byte{"encrypted": value, "plain": value}, data)
}

func TestSecretResolverRejectsInvalidSecrets(t *testing.T) {
	tests := []struct {
		name     string
		decoders []SecretDecoder
		secret   *compose.Secret
		expected string
	}{
		{
			name:     "no matching decoder",
			decoders: []SecretDecoder{NewAgeDecoder(nil)},
			secret:   &compose.Secret{File: "plain.txt", Data: []byte("ordinary")},
			expected: "no decoder for secret file",
		},
		{
			name:     "content was not loaded",
			decoders: []SecretDecoder{PlaintextDecoder{}},
			secret:   &compose.Secret{File: "plain.txt"},
			expected: "file content was not loaded",
		},
		{
			name:     "file combined with driver",
			decoders: []SecretDecoder{PlaintextDecoder{}},
			secret:   &compose.Secret{File: "plain.txt", Data: []byte("ordinary"), Driver: "vault"},
			expected: "cannot combine a secret file with an external driver",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewSecretResolver(tt.decoders...)

			_, err := resolver.Resolve("app", filepath.Join(t.TempDir(), "compose.yaml"), compose.Secrets{"plain": tt.secret})

			require.ErrorContains(t, err, tt.expected)
		})
	}
}
