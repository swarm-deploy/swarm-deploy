package githosting

import "context"

// CreateMergeRequestRequest is a merge request creation request.
type CreateMergeRequestRequest struct {
	// RepositoryURL is a remote git repository URL.
	RepositoryURL string
	// BaseBranch is a target branch where merge request should be merged.
	BaseBranch string
	// HeadBranch is a source branch with changes.
	HeadBranch string
	// Title is a merge request title.
	Title string
	// Body is a merge request body.
	Body string
	// Token is an API token for git hosting provider.
	Token string
}

// Provider creates merge requests for a specific git hosting.
type Provider interface {
	// Supports reports whether provider supports repository URL.
	Supports(repositoryURL string) bool
	// CreateMergeRequest creates a merge request and returns its URL.
	CreateMergeRequest(ctx context.Context, request CreateMergeRequestRequest) (string, error)
}
