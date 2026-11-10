package swarm

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// ErrSecretAlreadyExists means that a secret with this name already exists.
var ErrSecretAlreadyExists = errors.New("secret already exists")

// DockerUnavailableError marks a temporary Docker API availability or contention failure.
type DockerUnavailableError struct {
	cause error
}

// NewDockerUnavailableError marks a temporary Docker failure while preserving its cause.
func NewDockerUnavailableError(cause error) *DockerUnavailableError {
	return &DockerUnavailableError{cause: cause}
}

func (e *DockerUnavailableError) Error() string { return e.cause.Error() }

// Unwrap preserves the original Docker API error.
func (e *DockerUnavailableError) Unwrap() error { return e.cause }

// Temporary reports that the operation may succeed on a later retry.
func (*DockerUnavailableError) Temporary() bool { return true }

func classifyDockerError(err error) error {
	var unavailable *DockerUnavailableError
	var coded faults.Coded
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &unavailable) || errors.As(err, &coded) {
		return err
	}
	if cerrdefs.IsUnavailable(err) || isTemporaryDockerConnectionError(err) {
		return &DockerUnavailableError{cause: err}
	}
	diagnostic := err
	for errors.Unwrap(diagnostic) != nil {
		diagnostic = errors.Unwrap(diagnostic)
	}
	// Joined errors do not identify a single original Docker diagnostic.
	if _, joined := diagnostic.(interface{ Unwrap() []error }); joined {
		return err
	}
	if code, summary := dockerFault(diagnostic.Error()); code != "" {
		return &dockerFaultError{cause: err, code: code, summary: summary}
	}
	return err
}

type dockerFaultError struct {
	cause   error
	code    faults.Code
	summary string
}

func (e *dockerFaultError) Error() string          { return e.cause.Error() }
func (e *dockerFaultError) Unwrap() error          { return e.cause }
func (e *dockerFaultError) FaultCode() faults.Code { return e.code }
func (e *dockerFaultError) FaultSummary() string   { return e.summary }

func (e *dockerFaultError) Is(target error) bool {
	return target == ErrImageNotFound && e.code == faults.CodeImageNotFound
}

func dockerFault(diagnostic string) (faults.Code, string) {
	const maxSummaryLength = 256
	message := strings.ToLower(diagnostic)
	// A missing lease alone is not sufficient to identify an image extraction failure.
	if strings.Contains(message, "failed to prepare extraction snapshot") &&
		strings.Contains(message, "lease does not exist") {
		return faults.CodeImageExtractionFailed,
			"Container image extraction failed: containerd snapshot lease is missing. Retry deployment; check for concurrent image cleanup."
	}
	// Match Docker/Swarm diagnostics rather than generic permission or startup errors.
	for _, pattern := range []struct {
		text string
		code faults.Code
	}{
		{"no suitable node", faults.CodeNoSuitableNode},
		{"unauthorized: authentication required", faults.CodeImagePullDenied},
		{"denied: requested access to the resource is denied", faults.CodeImagePullDenied},
		{"no basic auth credentials", faults.CodeImagePullDenied},
		{"insufficient_scope: authorization failed", faults.CodeImagePullDenied},
		{"invalid mount config for type ", faults.CodeMountFailed},
		{"error while creating mount source path ", faults.CodeMountFailed},
		{"error while mounting volume ", faults.CodeMountFailed},
		{"error while creating volume ", faults.CodeMountFailed},
		{"volumedriver.mount:", faults.CodeMountFailed},
		{"failed to mount ", faults.CodeMountFailed},
		{"error mounting ", faults.CodeMountFailed},
		{"mount failed:", faults.CodeMountFailed},
	} {
		if index := strings.Index(message, pattern.text); index >= 0 {
			summary := strings.TrimSpace(diagnostic[index:])
			if len(summary) > maxSummaryLength {
				summary = summary[:maxSummaryLength] + "..."
			}
			return pattern.code, summary
		}
	}
	return "", ""
}

func isTemporaryDockerConnectionError(err error) bool {
	// An API response can definitively reject a write even if its body is truncated.
	if cerrdefs.IsInvalidArgument(err) || cerrdefs.IsUnauthorized(err) || cerrdefs.IsPermissionDenied(err) ||
		cerrdefs.IsNotFound(err) || isConflictErr(err) || cerrdefs.IsNotImplemented(err) || cerrdefs.IsInternal(err) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	// URL/network errors retain TLS, permission and configuration failures too.
	// Retry timeouts and temporary DNS failures, not every network error.
	var networkError net.Error
	var dnsError *net.DNSError
	if errors.As(err, &networkError) {
		return networkError.Timeout() || (errors.As(err, &dnsError) && dnsError.IsTemporary)
	}
	// The SDK discards the underlying error for connection refusals and timeouts.
	return client.IsErrConnectionFailed(err)
}

func isNotFoundErr(err error) bool {
	return cerrdefs.IsNotFound(err)
}

func isConflictErr(err error) bool {
	return cerrdefs.IsConflict(err) || cerrdefs.IsAlreadyExists(err)
}

func isUpdateConflict(err error) bool {
	// SwarmKit sequence conflicts may be returned as Unknown/HTTP 500.
	return cerrdefs.IsConflict(err) || (err != nil && strings.Contains(err.Error(), "update out of sequence"))
}

// ServiceUpdateRejected reports definite refusals, for which no write was accepted.
// Transport failures, deadlines and server errors may follow a committed write.
func ServiceUpdateRejected(err error) bool {
	return errors.Is(err, ErrServiceConflict) || errors.Is(err, ErrServiceNotFound) ||
		cerrdefs.IsInvalidArgument(err) || cerrdefs.IsPermissionDenied(err) ||
		cerrdefs.IsUnauthorized(err) || cerrdefs.IsNotImplemented(err)
}
