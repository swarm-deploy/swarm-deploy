// Package faults provides common classification of technical failures.
package faults

import "errors"

// Error is an error with a technical classification.
type Error interface {
	error

	// Temporary reports whether the failure may disappear on retry.
	Temporary() bool
}

type temporary interface {
	Temporary() bool
}

// causeTemporary reports temporariness of the cause: true if any error in the chain says so.
func causeTemporary(err error) bool {
	for err != nil {
		if t, ok := err.(temporary); ok { //nolint:errorlint // chain is walked manually
			return t.Temporary()
		}
		switch u := err.(type) { //nolint:errorlint // chain is walked manually
		case interface{ Unwrap() error }:
			err = u.Unwrap()
		default:
			return false
		}
	}
	return false
}

func message(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	return prefix + ": " + err.Error()
}

// IsTemporary reports whether err is a temporary failure.
func IsTemporary(err error) bool {
	var t temporary
	if errors.As(err, &t) {
		return t.Temporary()
	}
	return false
}

// TransportError is a network/transport failure. Temporary depends on the cause.
type TransportError struct {
	// Err is the underlying cause.
	Err error
}

func (e *TransportError) Error() string { return message("transport error", e.Err) }

// Unwrap returns the cause.
func (e *TransportError) Unwrap() error { return e.Err }

// Temporary reports whether the cause is temporary.
func (e *TransportError) Temporary() bool { return causeTemporary(e.Err) }

// TimeoutError is an operation timeout. Always temporary.
type TimeoutError struct {
	// Err is the underlying cause.
	Err error
}

func (e *TimeoutError) Error() string { return message("timeout", e.Err) }

// Unwrap returns the cause.
func (e *TimeoutError) Unwrap() error { return e.Err }

// Temporary always returns true.
func (e *TimeoutError) Temporary() bool { return true }

// Timeout marks the error as a timeout.
func (e *TimeoutError) Timeout() bool { return true }

// IOError is an input/output failure. Temporary depends on the cause.
type IOError struct {
	// Err is the underlying cause.
	Err error
}

func (e *IOError) Error() string { return message("io error", e.Err) }

// Unwrap returns the cause.
func (e *IOError) Unwrap() error { return e.Err }

// Temporary reports whether the cause is temporary.
func (e *IOError) Temporary() bool { return causeTemporary(e.Err) }

// UnavailableError means a dependency is unavailable. Always temporary.
type UnavailableError struct {
	// Err is the underlying cause.
	Err error
}

func (e *UnavailableError) Error() string { return message("unavailable", e.Err) }

// Unwrap returns the cause.
func (e *UnavailableError) Unwrap() error { return e.Err }

// Temporary always returns true.
func (e *UnavailableError) Temporary() bool { return true }

// RateLimitError means a request was rate limited. Always temporary.
type RateLimitError struct {
	// Err is the underlying cause.
	Err error
}

func (e *RateLimitError) Error() string { return message("rate limited", e.Err) }

// Unwrap returns the cause.
func (e *RateLimitError) Unwrap() error { return e.Err }

// Temporary always returns true.
func (e *RateLimitError) Temporary() bool { return true }

// DockerAPIError is a Docker API failure. Temporary depends on the cause.
type DockerAPIError struct {
	// Err is the underlying cause.
	Err error
}

func (e *DockerAPIError) Error() string { return message("docker api error", e.Err) }

// Unwrap returns the cause.
func (e *DockerAPIError) Unwrap() error { return e.Err }

// Temporary reports whether the cause is temporary.
func (e *DockerAPIError) Temporary() bool { return causeTemporary(e.Err) }
