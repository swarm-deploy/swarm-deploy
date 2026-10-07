package faults

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statusErr int

func (e statusErr) Error() string   { return fmt.Sprintf("status %d", int(e)) }
func (e statusErr) StatusCode() int { return int(e) }

func TestClassify(t *testing.T) {
	t.Parallel()

	refused := &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}}
	reset := &net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}

	cases := []struct {
		name     string
		err      error
		target   any
		temp     bool
		unmapped bool
	}{
		{"deadline", context.DeadlineExceeded, new(*TimeoutError), true, false},
		{"wrapped deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), new(*TimeoutError), true, false},
		{"os deadline", os.ErrDeadlineExceeded, new(*TimeoutError), true, false},
		{"rate limit", statusErr(429), new(*RateLimitError), true, false},
		{"bad gateway", statusErr(502), new(*UnavailableError), true, false},
		{"service unavailable", statusErr(503), new(*UnavailableError), true, false},
		{"refused", refused, new(*UnavailableError), true, false},
		{"reset", reset, new(*TransportError), true, false},
		{"unexpected eof", io.ErrUnexpectedEOF, new(*TransportError), false, false},
		{"plain", errors.New("boom"), nil, false, true},
		{"cancel", context.Canceled, nil, false, true},
		{"status 404", statusErr(404), nil, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := Classify(tc.err)
			require.ErrorIs(t, got, tc.err)
			if tc.unmapped {
				assert.Equal(t, tc.err, got)
				assert.False(t, IsTemporary(got) && !errors.Is(got, context.Canceled))
				return
			}
			require.ErrorAs(t, got, tc.target)
			assert.Equal(t, tc.temp, IsTemporary(got))
		})
	}
}

func TestClassifyNilAndIdempotent(t *testing.T) {
	t.Parallel()

	assert.NoError(t, Classify(nil))
	first := Classify(context.DeadlineExceeded)
	assert.Equal(t, first, Classify(first))
}

func TestWrapIO(t *testing.T) {
	t.Parallel()

	assert.NoError(t, WrapIO(nil))

	_, err := os.ReadFile("/nonexistent/file")
	got := WrapIO(err)
	var ioErr *IOError
	require.ErrorAs(t, got, &ioErr)
	require.ErrorIs(t, got, os.ErrNotExist)
	assert.False(t, ioErr.Temporary())
	assert.Equal(t, got, WrapIO(got))

	timeout := WrapIO(os.ErrDeadlineExceeded)
	var te *TimeoutError
	require.ErrorAs(t, timeout, &te)
	assert.True(t, IsTemporary(timeout))
}
