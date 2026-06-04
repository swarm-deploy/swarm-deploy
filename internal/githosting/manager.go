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

const (
	githubPathParts = 2
)

func (m *ProviderManager) Get(uri string) (*ReferencedProvider, error) {
	repoURI, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse uri: %w", err)
	}

	var (
		repoRef  RepositoryReference
		provider Provider
	)

	switch repoURI.Host {
	case "github.com":
		repoRef, err = m.resolveGithubRepositoryReference(repoURI)
		if err != nil {
			return nil, fmt.Errorf("resolve github repository reference: %w", err)
		}

		provider = m.github
	default:
		return nil, ErrProviderNotSupported
	}

	return NewReferencedProvider(provider, repoRef), nil
}

func (m *ProviderManager) resolveGithubRepositoryReference(repoURI *url.URL) (RepositoryReference, error) {
	parts := strings.SplitN(repoURI.Path, "/", githubPathParts)
	if len(parts) != githubPathParts {
		return RepositoryReference{}, errors.New("url not has repo name")
	}

	return RepositoryReference{
		Owner: parts[0],
		Name:  parts[1],
	}, nil
}
