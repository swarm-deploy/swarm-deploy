package githosting

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

const (
	githubHost      = "github.com"
	githubPathParts = 2
)

// RepositoryReference identifies a repository on its hosting service.
type RepositoryReference struct {
	// Owner is the repository namespace, including subgroups for nested repositories.
	Owner string
	// Name is the repository name without the clone suffix.
	Name   string
	origin url.URL
}

func parseRepositoryReference(raw string, fromWebPage bool) RepositoryReference {
	raw = strings.TrimSpace(raw)
	if fromWebPage && strings.HasPrefix(strings.ToLower(strings.TrimLeft(raw, "/")), githubHost+"/") {
		raw = "https://" + strings.TrimLeft(raw, "/")
	}
	// go-git includes query and fragment in Endpoint.Path. Neither is part of the repository name.
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	endpoint, err := transport.NewEndpoint(raw)
	if err != nil || endpoint.Host == "" {
		return RepositoryReference{}
	}
	origin := url.URL{Host: endpoint.Host, Scheme: endpoint.Protocol}
	switch endpoint.Protocol {
	case "git", "ssh":
		origin.Scheme = "https"
	case "http", "https":
		if endpoint.Port != 0 {
			origin.Host += ":" + strconv.Itoa(endpoint.Port)
		}
	default:
		return RepositoryReference{}
	}
	repo := RepositoryReference{origin: origin}
	parts := strings.Split(strings.Trim(endpoint.Path, "/"), "/")
	if fromWebPage && repo.isGitHub() && len(parts) > githubPathParts {
		parts = parts[:githubPathParts]
	}
	last := len(parts) - 1
	repo.Owner = strings.Join(parts[:last], "/")
	repo.Name = strings.TrimSuffix(parts[last], ".git")
	return repo
}

func (r RepositoryReference) isGitHub() bool {
	return strings.EqualFold(r.origin.Host, githubHost)
}

func (r RepositoryReference) validateGitHubPath() error {
	if r.Owner == "" || r.Name == "" || strings.Contains(r.Owner, "/") {
		return errors.New("expected a GitHub owner and repository name")
	}
	return nil
}

// ResolvePushRepository parses the write repository and configures its optional GitHub client.
func ResolvePushRepository(remote, token string) (RepositoryReference, *GithubProvider, error) {
	repo := parseRepositoryReference(remote, false)
	if token == "" || !repo.isGitHub() {
		return repo, nil, nil
	}
	client, err := NewGithubProvider(token)
	if err != nil {
		return RepositoryReference{}, nil, err
	}
	return repo, client, nil
}

// BranchURL returns a credential-free web link for a published branch, when available.
func (repo RepositoryReference) BranchURL(branch string) string {
	if repo.origin.Host == "" || repo.Name == "" {
		return ""
	}
	if strings.EqualFold(repo.origin.Hostname(), "gitlab.com") {
		return repo.origin.JoinPath(repo.Owner, repo.Name, "-", "tree", branch).String()
	}
	return repo.origin.JoinPath(repo.Owner, repo.Name, "tree", branch).String()
}
