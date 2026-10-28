package authenticator

import "errors"

var ErrValueNotProvided = errors.New("value not provided")

type Authenticator interface {
	// Authenticate
	// Returns ErrValueNotProvided
	Authenticate(req *Request) error
}
