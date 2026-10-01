package authenticator

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

type ComposeAuthenticator struct {
	authenticators []Authenticator
}

func NewAuthenticator(cfg *config.WebhookSpec) (Authenticator, error) {
	tp, tracingEnabled := tracing.GetTracerProvider()

	authenticators := make([]Authenticator, 0, len(cfg.Auth))

	for _, method := range cfg.Auth {
		var authenticator Authenticator

		switch method.Type {
		case config.WebhookAuthTypeBearer:
			authenticator = NewBearerAuthenticator(method.Secret.Content)
		case config.WebhookAuthTypeGitHub:
			authenticator = NewGitHubAuthenticator(method.Secret.Content)
		case config.WebhookAuthTypeHeader:
			authenticator = NewHeaderAuthenticator(
				method.Header,
				method.Secret.Content,
			)
		default:
			return nil, fmt.Errorf("unknown authentication type %q", method.Type)
		}

		if tracingEnabled {
			authenticator = newTraceSpanAuthenticator(tp, authenticator, method.Type)
		}

		authenticators = append(authenticators, authenticator)
	}

	composeAuth := NewComposeAuthenticator(authenticators...)
	if tracingEnabled {
		composeAuth = newTraceStartAuthenticator(tp, composeAuth)
	}

	return composeAuth, nil
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
			if !errors.Is(err, ErrValueNotProvided) {
				slog.InfoContext(req.Request.Context(),
					"[webhook] the authenticator did not pass the request",
					slog.Any("err", err),
				)
			}

			continue
		}

		return nil
	}

	slog.WarnContext(req.Request.Context(),
		"[webhook] request rejected",
	)

	return errors.New("not authorized")
}
