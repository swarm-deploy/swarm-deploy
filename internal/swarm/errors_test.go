package swarm

import (
	"fmt"
	"net"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestClassifyDockerError(t *testing.T) {
	cause := &net.OpError{Op: "request", Net: "docker", Err: cerrdefs.ErrUnavailable}
	classified := classifyDockerError(cause)
	var unavailable *DockerUnavailableError
	require.ErrorAs(t, classified, &unavailable)
	assert.Same(t, cause, unavailable.Unwrap())
	assert.EqualError(t, classified, cause.Error())
	assert.ErrorIs(t, classified, cerrdefs.ErrUnavailable)
	var original *net.OpError
	require.ErrorAs(t, classified, &original)
	assert.Same(t, cause, original)
	var temporary faults.Error
	require.ErrorAs(t, classified, &temporary)
	assert.True(t, temporary.Temporary())

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "nil"},
		{name: "permanent", err: cerrdefs.ErrInvalidArgument},
		{name: "already classified", err: classified},
		{name: "context around classified", err: fmt.Errorf("apply service: %w", classified)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.err, classifyDockerError(tc.err))
		})
	}
}
