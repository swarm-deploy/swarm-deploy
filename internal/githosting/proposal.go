package githosting

import (
	"context"
	"strings"
)

// ProposalRemote links published branches and optionally opens GitHub pull requests.
type ProposalRemote struct {
	repo   RepositoryReference
	github *GithubProvider
}

// NewProposalRemote accepts the clone URL from the Git write configuration.
func NewProposalRemote(repository, token string) (*ProposalRemote, error) {
	remote := &ProposalRemote{repo: parseRepositoryReference(repository, false)}
	if token != "" && remote.repo.isGitHub() {
		client, err := NewGithubProvider(token)
		if err != nil {
			return nil, err
		}
		remote.github = client
	}
	return remote, nil
}

// BranchURL returns a credential-free web link for a published branch, when available.
func (r *ProposalRemote) BranchURL(branch string) string {
	repo := r.repo
	if repo.origin.Host == "" || repo.Name == "" {
		return ""
	}
	if strings.EqualFold(repo.origin.Hostname(), "gitlab.com") {
		return repo.origin.JoinPath(repo.Owner, repo.Name, "-", "tree", branch).String()
	}
	return repo.origin.JoinPath(repo.Owner, repo.Name, "tree", branch).String()
}

// OpenPullRequest returns an empty URL when pull requests are not configured for this remote.
func (r *ProposalRemote) OpenPullRequest(ctx context.Context, base, head, title, body string) (string, error) {
	if r.github == nil {
		return "", nil
	}
	return r.github.CreatePullRequest(ctx, r.repo, base, head, title, body)
}
