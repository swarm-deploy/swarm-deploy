package githosting

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/go-github/v88/github"
)

type GithubProvider struct {
	client *github.Client
}

func NewGithubProvider(token string) (provider *GithubProvider, err error) {
	provider = &GithubProvider{}

	if token == "" {
		provider.client, err = github.NewClient()
		if err != nil {
			return nil, err
		}
	} else {
		provider.client, err = github.NewClient(github.WithAuthToken(token))
		if err != nil {
			return nil, err
		}
	}

	return provider, nil
}

func (g *GithubProvider) GetLatestRelease(ctx context.Context, repo RepositoryReference) (*Release, error) {
	release, resp, err := g.client.Repositories.GetLatestRelease(ctx, repo.Owner, repo.Name)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, ErrReleaseNotFound
		}
		return nil, err
	}

	return g.mapRelease(release), nil
}

// CreateChangeRequest opens a pull request between two branches of the same GitHub repository.
func (g *GithubProvider) CreateChangeRequest(
	ctx context.Context, repo RepositoryReference, base, head, title, body string,
) (ChangeRequest, error) {
	if err := repo.validateGitHubPath(); err != nil {
		return ChangeRequest{}, err
	}
	pr, _, err := g.client.PullRequests.Create(ctx, repo.Owner, repo.Name, &github.NewPullRequest{
		Base: &base, Head: &head, Title: &title, Body: &body,
	})
	if err != nil {
		return ChangeRequest{}, err
	}
	if pr.GetHTMLURL() == "" {
		return ChangeRequest{}, errors.New("GitHub returned a pull request without a URL")
	}
	return ChangeRequest{URL: pr.GetHTMLURL(), Number: pr.GetNumber()}, nil
}

func (g *GithubProvider) mapRelease(release *github.RepositoryRelease) *Release {
	unptrStr := func(val *string) string {
		if val == nil {
			return ""
		}
		return *val
	}
	unptrTimestamp := func(val *github.Timestamp) time.Time {
		if val == nil {
			return time.Time{}
		}
		return val.Time
	}

	return &Release{
		Tag:         unptrStr(release.TagName),
		Commit:      unptrStr(release.TargetCommitish),
		Body:        unptrStr(release.Body),
		URL:         unptrStr(release.HTMLURL),
		PublishedAt: unptrTimestamp(release.PublishedAt),
	}
}
