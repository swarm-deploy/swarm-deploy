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
)

// ErrSecretAlreadyExists means that a secret with this name already exists.
var ErrSecretAlreadyExists = errors.New("secret already exists")

// DockerUnavailableError marks a temporary Docker API availability failure.
type DockerUnavailableError struct {
	cause error
}

func (e *DockerUnavailableError) Error() string { return e.cause.Error() }

// Unwrap preserves the original Docker API error.
func (e *DockerUnavailableError) Unwrap() error { return e.cause }

// Temporary reports that the operation can be retried when Docker is available.
func (*DockerUnavailableError) Temporary() bool { return true }

func classifyDockerError(err error) error {
	var unavailable *DockerUnavailableError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &unavailable) {
		return err
	}
	if cerrdefs.IsUnavailable(err) || isTemporaryDockerConnectionError(err) {
		return &DockerUnavailableError{cause: err}
	}
	return err
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
