package secrets

import (
	"bytes"
	"io"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func encryptTestSecret(t *testing.T, recipient age.Recipient, plaintext []byte, armored bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	var output io.Writer = &buf
	var encoded io.WriteCloser
	if armored {
		encoded = armor.NewWriter(output)
		output = encoded
	}
	w, err := age.Encrypt(output, recipient)
	require.NoError(t, err)
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	if encoded != nil {
		require.NoError(t, encoded.Close())
	}
	return buf.Bytes()
}

func TestDecryptAgeSecret(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	other, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	plaintext := []byte("private value\x00\nwith binary data\xff")
	ciphertext := encryptTestSecret(t, identity.Recipient(), plaintext, false)
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 1
	for _, tt := range []struct {
		name       string
		ciphertext []byte
		identities string
		wantError  string
	}{
		{"binary", ciphertext, identity.String(), ""},
		{"armor", encryptTestSecret(t, identity.Recipient(), plaintext, true), identity.String(), ""},
		{"multiple identities and comments", ciphertext, "# identities\n\n" + other.String() + "\n" + identity.String() + "\n", ""},
		{"missing key", ciphertext, "", "requires secretRotation.age.identityFile"},
		{"empty key file", ciphertext, "# no identities\n", "invalid age identity file"},
		{"invalid key", ciphertext, identity.String() + "private-marker", "invalid age identity file"},
		{"wrong key", ciphertext, other.String(), "no matching identity"},
		{"invalid ciphertext", []byte("private-marker\n"), identity.String(), "invalid ciphertext"},
		{"invalid armor", []byte(armor.Header + "\nprivate-marker"), identity.String(), "invalid ciphertext"},
		{"truncated", ciphertext[:len(ciphertext)-1], identity.String(), "invalid or truncated payload"},
		{"tampered", tampered, identity.String(), "invalid or truncated payload"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, decryptErr := NewAgeDecoder([]byte(tt.identities)).Decode(tt.ciphertext)
			if tt.wantError == "" {
				require.NoError(t, decryptErr)
				assert.Equal(t, plaintext, got)
				return
			}
			require.ErrorContains(t, decryptErr, tt.wantError)
			assert.Nil(t, got, "never return partial plaintext")
			assert.NotContains(t, decryptErr.Error(), "private-marker")
			assert.NotContains(t, decryptErr.Error(), identity.String())
			assert.NotContains(t, decryptErr.Error(), string(plaintext))
		})
	}
}

func TestDecryptAgeSecretSizeLimit(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	for _, size := range []int{MaxSecretSize, MaxSecretSize + 1} {
		ciphertext := encryptTestSecret(t, identity.Recipient(), bytes.Repeat([]byte("x"), size), false)
		got, decryptErr := NewAgeDecoder([]byte(identity.String())).Decode(ciphertext)
		if size <= MaxSecretSize {
			require.NoError(t, decryptErr)
			assert.Len(t, got, size)
		} else {
			require.ErrorContains(t, decryptErr, "500 KiB limit")
			assert.Nil(t, got)
		}
	}
}
