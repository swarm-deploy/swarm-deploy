package swarm

import (
	"context"
	"errors"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
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
		!cerrdefs.IsUnavailable(err) || errors.As(err, &unavailable) {
		return err
	}
	return &DockerUnavailableError{cause: err}
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
