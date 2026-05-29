package authenticator

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"

	"github.com/artarts36/specw"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/tg123/go-htpasswd"
)

const basicAuthChallengeHeader = `Basic realm="swarm-deploy", charset="UTF-8"`

type basicAuthenticator struct {
	credentials *htpasswd.File
}

func newBasicAuthenticator(htpasswdFile specw.File) (Authenticator, error) {
	credentials, err := newBcryptHTPasswdFile(htpasswdFile)
	if err != nil {
		return nil, err
	}

	return &basicAuthenticator{
		credentials: credentials,
	}, nil
}

func (s *basicAuthenticator) Authenticate(r *http.Request) (security.User, bool) {
	username, password, ok := r.BasicAuth()
	if !ok {
		return security.User{}, false
	}

	matched := s.credentials.Match(username, password)
	if !matched {
		return security.User{}, false
	}

	return security.User{
		Name: username,
	}, true
}

func (s *basicAuthenticator) Challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", basicAuthChallengeHeader)
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

func newBcryptHTPasswdFile(authFile specw.File) (*htpasswd.File, error) {
	var parseErrs []error
	acceptedEntries := 0

	bcryptOnlyParser := func(src string) (htpasswd.EncodedPasswd, error) {
		matcher, err := htpasswd.AcceptBcrypt(src)
		if err != nil {
			return nil, err
		}
		if matcher != nil {
			acceptedEntries++
		}
		return matcher, nil
	}

	file, err := htpasswd.NewFromReader(
		bytes.NewBuffer(authFile.Content),
		[]htpasswd.PasswdParser{bcryptOnlyParser},
		func(err error) {
			parseErrs = append(parseErrs, err)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("open htpasswd file %s: %w", authFile, err)
	}

	if len(parseErrs) > 0 {
		return nil, fmt.Errorf("parse htpasswd file %s: only bcrypt is supported: %w", authFile, errors.Join(parseErrs...))
	}
	if acceptedEntries == 0 {
		return nil, fmt.Errorf("parse htpasswd file %s: %w",
			authFile,
			errors.New("htpasswd file does not contain credentials"),
		)
	}

	return file, nil
}
