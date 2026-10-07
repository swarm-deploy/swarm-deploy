package githosting

import (
	"context"
	"errors"
)

var ErrProviderNotSupported = errors.New("provider not supported")

type Provider interface {
	// GetLatestRelease returns latest from the remote repository.
	// Throws ErrReleaseNotFound.
	GetLatestRelease(ctx context.Context, repo RepositoryReference) (*Release, error)
}
