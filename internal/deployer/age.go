package deployer

import (
	"bytes"
	"errors"
	"io"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// Docker Engine limits the plaintext payload of a secret to 500 KiB.
const maxSecretSize = 500 * 1024

func decryptAgeSecret(ciphertext, identityContent []byte) ([]byte, error) {
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
