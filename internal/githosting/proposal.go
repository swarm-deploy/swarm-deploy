package githosting

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// ProposalRemote links published branches and optionally opens GitHub pull requests.
type ProposalRemote struct {
	web    *url.URL
	github *GithubProvider
}

// NewProposalRemote accepts the clone URL from the Git write configuration.
func NewProposalRemote(repository, token string) (*ProposalRemote, error) {
	remote := &ProposalRemote{web: webRepositoryURL(repository)}
	if remote.web != nil && strings.EqualFold(remote.web.Host, "github.com") && token != "" {
		client, err := NewGithubProvider(token)
		if err != nil {
			return nil, err
		}
		remote.github = client
	}
	return remote, nil
}

// BranchURL returns the browser URL, or an empty string for a local repository.
func (r *ProposalRemote) BranchURL(branch string) string {
	if r.web == nil {
		return ""
	}
	if strings.EqualFold(r.web.Hostname(), "gitlab.com") {
		return r.web.JoinPath("-", "tree", branch).String()
	}
	return r.web.JoinPath("tree", branch).String()
}

// OpenPullRequest returns an empty URL when pull requests are not configured for this remote.
func (r *ProposalRemote) OpenPullRequest(ctx context.Context, base, head, title, body string) (string, error) {
	if r.github == nil {
		return "", nil
	}
	owner, name, ok := strings.Cut(strings.Trim(r.web.Path, "/"), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", errors.New("expected a GitHub owner and repository name")
	}
	return r.github.CreatePullRequest(ctx, RepositoryReference{Owner: owner, Name: name}, base, head, title, body)
}

func webRepositoryURL(remote string) *url.URL {
	if !strings.Contains(remote, "://") {
		_, address, hasUser := strings.Cut(remote, "@")
		host, repo, ok := strings.Cut(address, ":")
		if !hasUser || !ok {
			return nil
		}
		remote = "ssh://" + host + "/" + strings.TrimPrefix(repo, "/")
	}
	repositoryURL, err := url.Parse(remote)
	if err != nil || repositoryURL.Hostname() == "" || repositoryURL.Path == "" {
		return nil
	}
	switch repositoryURL.Scheme {
	case "http", "https":
	case "ssh", "git":
		repositoryURL.Scheme = "https"
		repositoryURL.Host = repositoryURL.Hostname()
	default:
		return nil
	}
	return &url.URL{
		Scheme: repositoryURL.Scheme, Host: repositoryURL.Host,
		Path: strings.TrimSuffix(strings.TrimRight(repositoryURL.Path, "/"), ".git"),
	}
}
