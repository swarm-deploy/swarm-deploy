package authenticator

import (
	"crypto/hmac"
	"errors"
)

type HmacAuthenticator struct {
	header string
	secret []byte
}

func NewHmacAuthenticator(header string, secret []byte) *HmacAuthenticator {
	return &HmacAuthenticator{header: header, secret: secret}
}

func (b *HmacAuthenticator) Authenticate(req *Request) error {
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

