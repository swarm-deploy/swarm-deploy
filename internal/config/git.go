package config

import (
	"errors"
	"fmt"

	"github.com/artarts36/specw"
	"gopkg.in/yaml.v3"
)

type GitSpec struct {
	// Pull contains git repository settings used for read operations.
	Pull GitPullSpec `yaml:"pull"`
	// Push contains git repository settings used for write operations.
	Push GitPushSpec `yaml:"push"`
}

type GitPullSpec struct {
	// Repository is a git repository URL (ssh or https).
	Repository string `yaml:"repository"`
	// Branch is a git branch to track.
	Branch string `yaml:"branch"`
	// Auth contains git authentication settings.
	Auth GitAuthSpec `yaml:"auth"`
}

type GitPushSpec struct {
	// Repository is a git repository URL (ssh or https).
	Repository string `yaml:"repository"`
	// Branch is a git branch to push.
	Branch string `yaml:"branch"`
	// Auth contains git authentication settings.
	Auth GitAuthSpec `yaml:"auth"`
	// APIToken is an optional API token used by push integrations.
	APIToken specw.File `yaml:"apiToken,omitempty"`
}

type GitAuthSpec struct {
	// Type is git auth type: none, http, or ssh.
	Type string `yaml:"type"`
	// HTTP is HTTP(S) basic/token authentication configuration.
	HTTP GitHTTPAuth `yaml:"http"`
	// SSH is SSH authentication configuration.
	SSH GitSSHAuthSpec `yaml:"ssh"`
}

type GitHTTPAuth struct {
	// Username is HTTP basic auth username.
	Username string `yaml:"username"`
	// Password is HTTP basic auth password.
	//nolint:gosec // Field name is part of a user-facing config schema and does not imply hardcoded secret usage.
	Password string `yaml:"password"`
	// PasswordEnv is an env variable name containing HTTP password.
	PasswordEnv string `yaml:"passwordEnv"`
	// Token is an HTTP token value used as password.
	Token string `yaml:"token"`
	// TokenEnv is an env variable name containing HTTP token.
	TokenEnv string `yaml:"tokenEnv"`
}

type GitSSHAuthSpec struct {
	// User is an SSH user, typically "git".
	User string `yaml:"user"`
	// PrivateKeyPath is a path to a private key file for git SSH auth.
	PrivateKeyPath string `yaml:"privateKeyPath"`
	// KnownHostsPath is a path to known_hosts file used for host verification.
	KnownHostsPath string `yaml:"knownHostsPath"`
	// InsecureIgnoreHostKey disables SSH host key verification.
	InsecureIgnoreHostKey bool `yaml:"insecureIgnoreHostKey"`
	// Passphrase is a path to file containing private key passphrase.
	Passphrase specw.File `yaml:"passphrasePath,omitempty"`
}

func (s *GitSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return errors.New("git config must be a map")
	}

	repositoryNode, hasRepository := findYAMLMappingValue(node, "repository")
	if hasRepository {
		if repositoryNode.Kind == yaml.ScalarNode {
			return s.unmarshalSingleRepository(node)
		}
		return errors.New("git.repository as object is not supported; use git.pull and git.push")
	}

	var topLevelSpec struct {
		Pull *GitPullSpec `yaml:"pull"`
		Push *GitPushSpec `yaml:"push"`
	}
	if err := node.Decode(&topLevelSpec); err != nil {
		return fmt.Errorf("decode git: %w", err)
	}
	if topLevelSpec.Pull != nil || topLevelSpec.Push != nil {
		if topLevelSpec.Pull == nil {
			return errors.New("git.pull is required when git.push is set")
		}
		if topLevelSpec.Push == nil {
			return errors.New("git.push is required when git.pull is set")
		}

		s.Pull = *topLevelSpec.Pull
		s.Push = *topLevelSpec.Push
		return nil
	}

	return s.unmarshalSingleRepository(node)
}

func (s *GitSpec) unmarshalSingleRepository(node *yaml.Node) error {
	var singleSpec struct {
		Repository string      `yaml:"repository"`
		Branch     string      `yaml:"branch"`
		Auth       GitAuthSpec `yaml:"auth"`
	}
	if err := node.Decode(&singleSpec); err != nil {
		return fmt.Errorf("unmarshal single spec: %w", err)
	}

	s.Pull = GitPullSpec{
		Repository: singleSpec.Repository,
		Branch:     singleSpec.Branch,
		Auth:       singleSpec.Auth,
	}
	s.Push = GitPushSpec{
		Repository: singleSpec.Repository,
		Branch:     singleSpec.Branch,
		Auth:       singleSpec.Auth,
	}

	return nil
}

func findYAMLMappingValue(node *yaml.Node, key string) (*yaml.Node, bool) {
	if node.Kind != yaml.MappingNode {
		return nil, false
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1], true
		}
	}

	return nil, false
}

func (a GitHTTPAuth) ResolvePassword() string {
	if a.Token != "" {
		return a.Token
	}
	return a.Password
}

func (a GitHTTPAuth) ResolveUsername() string {
	if a.Username != "" {
		return a.Username
	}
	if a.Token != "" || a.TokenEnv != "" {
		// go-git basic auth requires non-empty username when token is used.
		return "oauth2"
	}
	return ""
}
