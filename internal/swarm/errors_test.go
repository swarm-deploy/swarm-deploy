package swarm

import (
	"context"
	"errors"
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
		{name: "canceled", err: fmt.Errorf("request: %w", context.Canceled)},
		{name: "deadline", err: fmt.Errorf("request: %w", context.DeadlineExceeded)},
		{name: "unavailable with cancellation", err: errors.Join(cerrdefs.ErrUnavailable, context.Canceled)},
		{name: "unavailable with deadline", err: errors.Join(cerrdefs.ErrUnavailable, context.DeadlineExceeded)},
		{name: "already classified", err: classified},
		{name: "context around classified", err: fmt.Errorf("apply service: %w", classified)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.err, classifyDockerError(tc.err))
		})
	}
}

func TestDockerCreateConflictClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"conflict", cerrdefs.ErrConflict, true},
		{"already exists", fmt.Errorf("Docker create: %w", cerrdefs.ErrAlreadyExists), true},
		{"unavailable", cerrdefs.ErrUnavailable, false},
		{"invalid", cerrdefs.ErrInvalidArgument, false},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, isConflictErr(tc.err)) })
	}
}
