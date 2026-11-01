package faults

import (
	"context"
	"errors"

	cerrdefs "github.com/containerd/errdefs"
)

// SanitizeError replaces all original diagnostics with a trusted message while
// preserving Temporary classification. It does not retain or unwrap the cause,
// which may contain sensitive request data.
func SanitizeError(err error, diagnostic string) error {
	if err == nil {
		return nil
	}
	var classified Error
	temporary := errors.Is(err, context.DeadlineExceeded) ||
		cerrdefs.IsUnavailable(err) ||
		(errors.As(err, &classified) && classified.Temporary())
	return sanitizedError{diagnostic: diagnostic, temporary: temporary}
}

type sanitizedError struct {
	diagnostic string
	temporary  bool
}

func (e sanitizedError) Error() string { return e.diagnostic }

func (e sanitizedError) Temporary() bool { return e.temporary }
