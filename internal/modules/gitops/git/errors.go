package git

import (
	"errors"

	"github.com/go-git/go-git/v5/plumbing"
)

// IsCommitNotFound reports whether commit hash does not exist in repository.
func IsCommitNotFound(err error) bool {
	return errors.Is(err, plumbing.ErrObjectNotFound)
}
