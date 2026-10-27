package deployer

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// AgeDecoder decrypts native age files without exposing key or payload errors.
type AgeDecoder struct {
	identity []byte
}

// NewAgeDecoder builds a decoder using identities loaded from a mounted file.
func NewAgeDecoder(identity []byte) *AgeDecoder {
	return &AgeDecoder{identity: identity}
}

// Matches selects age-encrypted secret files by extension.
func (*AgeDecoder) Matches(path string) bool {
	return filepath.Ext(path) == ".age"
}

// Docker Engine limits the plaintext payload of a secret to 500 KiB.
const maxSecretSize = 500 * 1024

// Decode authenticates and decrypts the complete secret payload in memory.
func (d *AgeDecoder) Decode(ciphertext []byte) ([]byte, error) {
	identityContent := d.identity
	if len(identityContent) == 0 {
		return nil, errors.New("age secret requires secretRotation.age.identityFile")
	}
	identities, err := age.ParseIdentities(bytes.NewReader(identityContent))
	if err != nil || len(identities) == 0 {
		// Parser errors can contain the offending identity line.
		return nil, errors.New("invalid age identity file")
	}

	var input io.Reader = bytes.NewReader(ciphertext)
	if bytes.HasPrefix(ciphertext, []byte(armor.Header)) {
		input = armor.NewReader(input)
	}
	plaintext, err := age.Decrypt(input, identities...)
	if err != nil {
		// Invalid headers can include arbitrary data from the encrypted file.
		return nil, errors.New("cannot decrypt age secret: invalid ciphertext or no matching identity")
	}
	data, err := io.ReadAll(io.LimitReader(plaintext, maxSecretSize+1))
	if err != nil {
		clear(data)
		return nil, errors.New("cannot decrypt age secret: invalid or truncated payload")
	}
	if len(data) > maxSecretSize {
		clear(data)
		return nil, errors.New("decrypted age secret exceeds Docker's 500 KiB limit")
	}
	return data, nil
}
