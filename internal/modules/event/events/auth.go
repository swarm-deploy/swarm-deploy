package events

import "fmt"

// UserAuthenticated is emitted when a user passes web authentication.
type UserAuthenticated struct {
	Username string
}

func (u *UserAuthenticated) Type() Type {
	return TypeUserAuthenticated
}

func (u *UserAuthenticated) Message() string {
	if u.Username == "" {
		return "User authenticated"
	}

	return fmt.Sprintf("User %s authenticated", u.Username)
}

func (u *UserAuthenticated) Details() map[string]string {
	return map[string]string{
		"username": u.Username,
	}
}
