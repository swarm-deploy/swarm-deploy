package githosting

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type ProviderManager struct {
	github *GithubProvider
}

type Config struct {
	GitHub struct {
		Token string `yaml:"token"`
	} `yaml:"github"`
}

func NewProviderManager(cfg Config) (*ProviderManager, error) {
	githubProvider, err := NewGithubProvider(cfg.GitHub.Token)
	if err != nil {
		return nil, fmt.Errorf("create github client: %w", err)
	}

	return &ProviderManager{
		github: githubProvider,
	}, nil
}

const githubPathParts = 2

func (m *ProviderManager) Get(uri string) (*ReferencedProvider, error) {
	uri = strings.TrimSpace(uri)
	repoURI, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse uri: %w", err)
	}
	if repoURI.Host == "" && repoURI.Scheme == "" {
		repoURI, err = url.Parse("https://" + strings.Trim(uri, "/"))
		if err != nil {
			return nil, fmt.Errorf("parse uri without scheme: %w", err)
		}
	}

	if !strings.EqualFold(repoURI.Host, githubHost) {
		return nil, ErrProviderNotSupported
	}
	repoRef, err := parseGithubRepositoryReference(repoURI)
	if err != nil {
		return nil, fmt.Errorf("resolve github repository reference: %w", err)
	}

	return NewReferencedProvider(m.github, repoRef), nil
}

func parseGithubRepositoryReference(repoURI *url.URL) (RepositoryReference, error) {
	trimmedPath := strings.Trim(strings.TrimSpace(repoURI.Path), "/")
	parts := strings.Split(trimmedPath, "/")
	if len(parts) < githubPathParts || parts[0] == "" || parts[1] == "" {
		return RepositoryReference{}, errors.New("expected a GitHub owner and repository name")
	}
	name := strings.TrimSuffix(parts[1], ".git")
	if name == "" {
		return RepositoryReference{}, errors.New("expected a GitHub owner and repository name")
	}

	return RepositoryReference{
		Owner: parts[0],
		Name:  name,
	}, nil
}
