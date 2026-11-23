package authenticator

import (
	"crypto/hmac"
	"errors"
)

type HeaderAuthenticator struct {
	header string
	secret []byte
}

func NewHeaderAuthenticator(header string, secret []byte) *HeaderAuthenticator {
	return &HeaderAuthenticator{header: header, secret: secret}
}

func (b *HeaderAuthenticator) Authenticate(req *Request) error {
	headerValue := req.Request.Header.Get(b.header)
	if headerValue == "" {
		return ErrValueNotProvided
	}

	matched := hmac.Equal([]byte(headerValue), b.secret)
	if !matched {
		return errors.New("unexpected value")
	}

	return nil
}
