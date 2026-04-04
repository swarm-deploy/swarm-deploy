package config

import (
	"strings"

	"github.com/artarts36/specw"
)

// GitAuthType defines supported git authentication type values.
type GitAuthType string

const (
	// GitAuthTypeNone disables git authentication.
	GitAuthTypeNone GitAuthType = "none"
	// GitAuthTypeHTTP enables HTTP(S) authentication.
	GitAuthTypeHTTP GitAuthType = "http"
	// GitAuthTypeSSH enables SSH key authentication.
	GitAuthTypeSSH GitAuthType = "ssh"
)

// IsSupported reports whether auth type is one of supported enum values.
func (t GitAuthType) IsSupported() bool {
	switch t {
	case "", GitAuthTypeNone, GitAuthTypeHTTP, GitAuthTypeSSH:
		return true
	default:
		return false
	}
}

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
	Type GitAuthType `yaml:"type"`
	// HTTP is HTTP(S) basic/token authentication configuration.
	HTTP GitHTTPAuth `yaml:"http"`
	// SSH is SSH authentication configuration.
	SSH GitSSHAuthSpec `yaml:"ssh"`
}

type GitHTTPAuth struct {
	// Username is HTTP basic auth username.
	Username string `yaml:"username"`
	// Password is a path to file containing HTTP basic auth password.
	Password specw.File `yaml:"passwordPath,omitempty"`
	// Token is a path to file containing HTTP token used as password.
	Token specw.File `yaml:"tokenPath,omitempty"`
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
	token := strings.TrimSpace(string(a.Token.Content))
	if token != "" {
		return token
	}

	return strings.TrimSpace(string(a.Password.Content))
}

func (a GitHTTPAuth) ResolveUsername() string {
	username := strings.TrimSpace(a.Username)
	if username != "" {
		return username
	}
	if a.Token.String() != "" {
		// go-git basic auth requires non-empty username when token is used.
		return "oauth2"
	}
	return ""
}
