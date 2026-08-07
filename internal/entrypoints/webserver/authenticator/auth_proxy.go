package authenticator

import (
	"errors"
	"net/http"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

type authProxyAuthenticator struct {
	loginHeader string
}

func newAuthProxyAuthenticator(loginHeader string) (Authenticator, error) {
	loginHeader = strings.TrimSpace(loginHeader)
	if loginHeader == "" {
		return nil, errors.New("login header is required")
	}

	return &authProxyAuthenticator{loginHeader: loginHeader}, nil
}

func (a *authProxyAuthenticator) Authenticate(r *http.Request) (security.User, bool) {
	login := strings.TrimSpace(r.Header.Get(a.loginHeader))
	if login == "" {
		return security.User{}, false
	}

	return security.User{Name: login}, true
}

func (*authProxyAuthenticator) Challenge(w http.ResponseWriter) {
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}
