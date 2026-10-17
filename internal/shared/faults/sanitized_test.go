package faults

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeErrorPreservesClassificationWithoutCause(t *testing.T) {
	for _, test := range []struct {
		name      string
		cause     error
		temporary bool
	}{
		{name: "wrapped timeout", cause: fmt.Errorf("private-request-marker: %w", &net.OpError{Op: "private-key-marker", Err: syscall.ETIMEDOUT}), temporary: true},
		{name: "permanent classified", cause: &net.OpError{Op: "private-key-marker", Err: errors.New("private-payload-marker")}},
		{name: "unclassified", cause: errors.New("private-payload-marker")},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := SanitizeError(test.cause, "Docker API request failed")
			require.EqualError(t, err, "Docker API request failed")
			var classified Error
			require.ErrorAs(t, fmt.Errorf("outer: %w", err), &classified)
			assert.Equal(t, test.temporary, classified.Temporary())
			assert.Nil(t, errors.Unwrap(err))
			assert.NotErrorIs(t, err, test.cause)
			assert.NotContains(t, fmt.Sprintf("%v %+v %#v", err, err, err), "private-")
		})
	}
	assert.Nil(t, SanitizeError(nil, "Docker API request failed"))
}
