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

// ChangeRequestCreator opens repository changes for review on a hosting provider.
type ChangeRequestCreator interface {
	// CreateChangeRequest opens a review request between two branches of the same repository.
	CreateChangeRequest(
		ctx context.Context, repo RepositoryReference, base, head, title, body string,
	) (ChangeRequest, error)
}

// ChangeRequest identifies a review request created by a hosting provider.
type ChangeRequest struct {
	// URL opens the change request in the hosting provider.
	URL string
	// Number is the provider-assigned change request number.
	Number int
}
