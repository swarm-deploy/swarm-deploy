package authenticator

import (
	"errors"
	"log/slog"
)

type ComposeAuthenticator struct {
	authenticators []Authenticator
}

func NewComposeAuthenticator(authenticators ...Authenticator) Authenticator {
	return &ComposeAuthenticator{
		authenticators: authenticators,
	}
}

func (c *ComposeAuthenticator) Authenticate(req *Request) error {
	for _, method := range c.authenticators {
		err := method.Authenticate(req)
		if err != nil {
			if errors.Is(err, ErrValueNotProvided) {
				continue
			}

			slog.WarnContext(req.Request.Context(), "[webhook] failed to authenticate", slog.Any("err", err))

			return err
		}

		return nil
	}

	return errors.New("not authorized")
}
