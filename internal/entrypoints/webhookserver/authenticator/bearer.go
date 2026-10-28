package authenticator

import (
	"crypto/hmac"
	"errors"
	"strings"
)

type BearerAuthenticator struct {
	secret []byte
}

func NewBearerAuthenticator(secret []byte) *BearerAuthenticator {
	return &BearerAuthenticator{secret: secret}
}

func (b *BearerAuthenticator) Authenticate(req *Request) error {
	headerValue := req.Request.Header.Get("Authorization")
	if headerValue == "" {
		return ErrValueNotProvided
	}

	parts := strings.Fields(headerValue)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return errors.New("invalid authorization header")
	}

	matched := hmac.Equal([]byte(parts[1]), b.secret)
	if !matched {
		return errors.New("unexpected value")
	}

	return nil
}
