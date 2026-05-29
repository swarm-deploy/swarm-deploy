package authenticator

import (
	"fmt"
	"net/http"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

type Authenticator interface {
	// Authenticate checks whether request credentials are valid.
	Authenticate(r *http.Request) (security.User, bool)
	// Challenge writes an unauthorized response with auth challenge headers.
	Challenge(w http.ResponseWriter)
}

func Create(cfg config.AuthenticationSpec) (Authenticator, error) {
	switch cfg.Strategy() {
	case config.AuthenticationStrategyNone:
		//nolint:nilnil // authentication not required
		return nil, nil
	case config.AuthenticationStrategyBasic:
		authenticator, err := newBasicAuthenticator(cfg.Basic.HTPasswdFile)
		if err != nil {
			return nil, fmt.Errorf("create basic authenticator: %w", err)
		}
		return authenticator, nil
	default:
		return nil, fmt.Errorf("unsupported authenticator %q", cfg.Strategy())
	}
}
