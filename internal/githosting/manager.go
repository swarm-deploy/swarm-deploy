package githosting

import "fmt"

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
