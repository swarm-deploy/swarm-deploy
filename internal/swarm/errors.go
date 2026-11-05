package swarm

import (
	"errors"

	cerrdefs "github.com/containerd/errdefs"
)

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
	if !cerrdefs.IsUnavailable(err) || errors.As(err, &unavailable) {
		return err
	}
	return &DockerUnavailableError{cause: err}
}
