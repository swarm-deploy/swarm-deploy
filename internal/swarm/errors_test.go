package swarm

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/httpx"
)

func TestMatchAPIErrors(t *testing.T) {
	tests := []struct {
		name      string
		sourceErr error
		assertErr func(t *testing.T, err error)
	}{
		{
			name:      "request canceled",
			sourceErr: context.Canceled,
			assertErr: func(t *testing.T, err error) {
				var target *httpx.RequestCanceledError
				require.ErrorAs(t, err, &target, "error must be matched as request canceled")
			},
		},
		{
			name:      "transport failure",
			sourceErr: errors.New("transport failure"),
			assertErr: func(t *testing.T, err error) {
				var target *httpx.TransportError
				require.ErrorAs(t, err, &target, "error must be matched as transport failure")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := make(chan error, 1)
			source <- tt.sourceErr
			close(source)

			matched := matchAPIErrors(source)
			err, ok := <-matched
			require.True(t, ok, "matched error channel must contain the source error")
			tt.assertErr(t, err)
			assert.ErrorIs(t, err, tt.sourceErr, "matched error must preserve the source error")
			_, ok = <-matched
			assert.False(t, ok, "matched error channel must be closed with the source channel")
		})
	}
}
