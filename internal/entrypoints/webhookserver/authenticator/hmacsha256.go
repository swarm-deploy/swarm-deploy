package authenticator

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

type HmacSHA256Authenticator struct {
	header string
	secret []byte
}

func NewHmacSHA256Authenticator(header string, secret []byte) *HmacSHA256Authenticator {
	return &HmacSHA256Authenticator{
		header: header,
		secret: secret,
	}
}

func NewGitHubAuthenticator(secret []byte) *HmacSHA256Authenticator {
	return NewHmacSHA256Authenticator("X-Hub-Signature-256", secret)
}

func (h *HmacSHA256Authenticator) Authenticate(req *Request) error {
	return h.validateSignature(
		req.Request.Header.Get(h.header),
		req.Body,
	)
}

func (h *HmacSHA256Authenticator) validateSignature(signature string, body []byte) error {
	if signature == "" {
		return ErrValueNotProvided
	}

	algorithm, encodedSignature, ok := strings.Cut(strings.TrimSpace(signature), "=")
	if !ok || algorithm != "sha256" {
		return fmt.Errorf("invalid secret, got: %q", algorithm)
	}

	actualSignature, err := hex.DecodeString(encodedSignature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	mac := hmac.New(sha256.New, h.secret)
	_, _ = mac.Write(body)

	matched := hmac.Equal(actualSignature, mac.Sum(nil))
	if !matched {
		return errors.New("unexpected signature")
	}

	return nil
}
