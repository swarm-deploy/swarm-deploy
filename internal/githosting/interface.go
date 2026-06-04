package githosting

import "context"

type Provider interface {
	// GetLatestRelease returns latest from the remote repository.
	// Throws ErrReleaseNotFound.
	GetLatestRelease(ctx context.Context, repo RepositoryReference) (*Release, error)
}
