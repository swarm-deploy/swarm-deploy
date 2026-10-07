package faults

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tempErr struct{ temp bool }

func (e tempErr) Error() string   { return "cause" }
func (e tempErr) Temporary() bool { return e.temp }

func TestFaults(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	cases := []struct {
		name string
		make func(error) Error
		err  error
		want bool
	}{
		{"transport plain", func(e error) Error { return &TransportError{Err: e} }, cause, false},
		{"transport temp", func(e error) Error { return &TransportError{Err: e} }, tempErr{true}, true},
		{"transport nontemp", func(e error) Error { return &TransportError{Err: e} }, tempErr{false}, false},
		{"io plain", func(e error) Error { return &IOError{Err: e} }, cause, false},
		{"io temp wrapped", func(e error) Error { return &IOError{Err: e} }, fmt.Errorf("w: %w", tempErr{true}), true},
		{"docker plain", func(e error) Error { return &DockerAPIError{Err: e} }, cause, false},
		{"docker temp", func(e error) Error { return &DockerAPIError{Err: e} }, tempErr{true}, true},
		{"timeout", func(e error) Error { return &TimeoutError{Err: e} }, cause, true},
		{"unavailable", func(e error) Error { return &UnavailableError{Err: e} }, cause, true},
		{"ratelimit", func(e error) Error { return &RateLimitError{Err: e} }, cause, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.make(tc.err)
			assert.Equal(t, tc.want, err.Temporary())
			assert.ErrorIs(t, err, tc.err)
			assert.Equal(t, tc.err, errors.Unwrap(err))
			assert.Equal(t, tc.want, IsTemporary(fmt.Errorf("outer: %w", err)))

			var fe Error
			require.ErrorAs(t, fmt.Errorf("outer: %w", err), &fe)
			assert.Equal(t, err, fe)
		})
	}
}

func TestErrorsAsConcreteAndDeepChain(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("op: %w", &DockerAPIError{Err: &TransportError{Err: context.DeadlineExceeded}})

	var d *DockerAPIError
	var tr *TransportError
	require.ErrorAs(t, err, &d)
	require.ErrorAs(t, err, &tr)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, d.Temporary())
}

func TestNilCause(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "transport error", (&TransportError{}).Error())
	assert.False(t, (&IOError{}).Temporary())
}
