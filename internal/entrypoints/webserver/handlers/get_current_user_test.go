package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

func TestHandlerGetCurrentUser(t *testing.T) {
	t.Parallel()

	ctx := security.ContextWithUser(context.Background(), security.User{Name: "alice"})
	h := &handler{}

	resp, err := h.GetCurrentUser(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "alice", resp.Name)
}

func TestHandlerGetCurrentUser_Unauthorized(t *testing.T) {
	t.Parallel()

	h := &handler{}

	_, err := h.GetCurrentUser(context.Background())
	require.Error(t, err)

	var sErr *statusError
	require.True(t, errors.As(err, &sErr))
	assert.Equal(t, 401, sErr.code)
}
