package config

import (
	"github.com/artarts36/specw"
)

type GitSpec struct {
	// Repository is a git repository URL (ssh or https).
	Repository string `yaml:"repository"`
	// Branch is a git branch to track.
	Branch string `yaml:"branch"`
	// Auth contains git authentication settings.
	Auth GitAuthSpec `yaml:"auth"`
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
