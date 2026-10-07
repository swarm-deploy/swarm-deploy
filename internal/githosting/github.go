package githosting

import (
	"context"
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
