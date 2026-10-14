package git

import (
	"context"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"golang.org/x/crypto/ssh/knownhosts"
)

// FailureCategory is a stable classification of a remote Git failure.
type FailureCategory string

const (
	FailureAuthentication      FailureCategory = "authentication"
	FailureAccessDenied        FailureCategory = "access_denied"
	FailureRepositoryNotFound  FailureCategory = "repository_not_found"
	FailureNetwork             FailureCategory = "network"
	FailureSSHHostVerification FailureCategory = "ssh_host_verification"
	FailureOther               FailureCategory = "other"
)

var (
	errAuthenticationConfiguration = errors.New("invalid Git authentication configuration")
	errSSHHostVerification         = errors.New("invalid SSH host verification configuration")
)

// PullError contains only a safe failure category and diagnostic.
// The underlying transport error is deliberately not retained because it may
// contain credentials embedded in a remote URL.
type PullError struct {
	category   FailureCategory
	diagnostic string
}

func (e *PullError) Error() string { return e.diagnostic }

// Category returns the stable remote failure classification.
func (e *PullError) Category() FailureCategory { return e.category }

// ClassifyPullError converts transport-specific failures to safe structured errors.
func ClassifyPullError(err error, repository string, authType config.GitAuthType) error {
	if err == nil {
		return nil
	}
	var classified *PullError
	if errors.As(err, &classified) {
		return classified
	}

	category := classifyFailure(err, repository, authType)
	return &PullError{category: category, diagnostic: failureDiagnostic(category)}
}

// PullFailure returns the classification and sanitized diagnostic for a pull failure.
func PullFailure(err error) (FailureCategory, string) {
	var classified *PullError
	if errors.As(err, &classified) {
		return classified.category, classified.diagnostic
	}
	return FailureOther, failureDiagnostic(FailureOther)
}

func classifyFailure(err error, repository string, authType config.GitAuthType) FailureCategory {
	switch {
	case errors.Is(err, errAuthenticationConfiguration):
		return FailureAuthentication
	case errors.Is(err, errSSHHostVerification):
		return FailureSSHHostVerification
	case errors.Is(err, transport.ErrAuthenticationRequired):
		return FailureAuthentication
	case errors.Is(err, transport.ErrAuthorizationFailed):
		return FailureAccessDenied
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return FailureRepositoryNotFound
	}

	var hostKeyError *knownhosts.KeyError
	if errors.As(err, &hostKeyError) {
		return FailureSSHHostVerification
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return FailureNetwork
	}

	// The SSH protocol does not provide structured Git server error codes. Limit
	// the fallback to SSH transport errors after typed network/host-key checks.
	if authType == config.GitAuthTypeSSH || isSSHRemote(repository) {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "unable to authenticate") ||
			strings.Contains(message, "no supported methods remain") ||
			strings.Contains(message, "permission denied (publickey") {
			return FailureAuthentication
		}
		var protocolError *pktline.ErrorLine
		if errors.As(err, &protocolError) {
			serverMessage := strings.ToLower(protocolError.Text)
			switch {
			case strings.Contains(serverMessage, "repository not found"),
				strings.Contains(serverMessage, "does not exist"):
				return FailureRepositoryNotFound
			case strings.Contains(serverMessage, "access denied"), strings.Contains(serverMessage, "permission denied"):
				return FailureAccessDenied
			}
		}
	}
	return FailureOther
}

func failureDiagnostic(category FailureCategory) string {
	switch category {
	case FailureAuthentication:
		return "Git repository authentication failed"
	case FailureAccessDenied:
		return "Access to Git repository was denied"
	case FailureRepositoryNotFound:
		return "Git repository was not found"
	case FailureNetwork:
		return "Git repository is temporarily unreachable"
	case FailureSSHHostVerification:
		return "SSH host verification failed"
	case FailureOther:
		return "Git pull failed"
	default:
		return "Git pull failed"
	}
}

// RepositoryIdentifier returns a stable credential-free repository identity.
func RepositoryIdentifier(raw string) string {
	raw = strings.TrimSpace(raw)
	if host, path, ok := splitSCPRemote(raw); ok {
		return canonicalHostPath(host, path)
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme != "" {
		if parsed.Scheme == "file" {
			return filepath.Clean(parsed.Path)
		}
		host := strings.ToLower(parsed.Hostname())
		if parsed.Port() != "" {
			host += ":" + parsed.Port()
		}
		return canonicalHostPath(host, parsed.Path)
	}
	if absolute, absoluteErr := filepath.Abs(raw); absoluteErr == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(raw)
}

func splitSCPRemote(raw string) (string, string, bool) {
	if strings.Contains(raw, "://") {
		return "", "", false
	}
	colon := strings.IndexByte(raw, ':')
	if colon <= 0 || strings.Contains(raw[:colon], "/") {
		return "", "", false
	}
	host := raw[:colon]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	return strings.ToLower(host), raw[colon+1:], host != ""
}

func canonicalHostPath(host, path string) string {
	path = strings.TrimSuffix(strings.Trim(strings.ReplaceAll(path, "\\", "/"), "/"), ".git")
	if host == "" {
		return path
	}
	if path == "" {
		return host
	}
	return host + "/" + path
}

func isSSHRemote(repository string) bool {
	if _, _, ok := splitSCPRemote(repository); ok {
		return true
	}
	parsed, err := url.Parse(repository)
	return err == nil && parsed.Scheme == "ssh"
}

// IsCommitNotFound reports whether commit hash does not exist in repository.
func IsCommitNotFound(err error) bool {
	return errors.Is(err, plumbing.ErrObjectNotFound)
}
