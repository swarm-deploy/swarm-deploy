package swarm

import (
	"errors"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// dockerAPIError marks an error returned by the Docker API client as faults.DockerAPIError.
// The technical nature of the failure (timeout, transport, unavailability) is classified
// by faults.Classify and exposed via Unwrap.
func dockerAPIError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr *faults.DockerAPIError
	if errors.As(err, &apiErr) {
		return err
	}

	if cerrdefs.IsUnavailable(err) || client.IsErrConnectionFailed(err) {
		return &faults.DockerAPIError{Err: &faults.UnavailableError{Err: err}}
	}

	return &faults.DockerAPIError{Err: faults.Classify(err)}
}
