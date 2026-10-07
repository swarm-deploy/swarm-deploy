package faults

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
)

// statusCoder is implemented by errors carrying an HTTP status code.
type statusCoder interface {
	StatusCode() int
}

// Classify wraps err into a shared fault based on its real underlying cause:
//   - deadline/timeout errors become TimeoutError;
//   - HTTP 429 becomes RateLimitError, HTTP 502/503/504 and refused connections become UnavailableError;
//   - other network errors become TransportError.
//
// Errors that are already a faults.Error, are not recognized, or are context cancellation are returned unchanged.
func Classify(err error) error {
	if err == nil {
		return nil
	}

	if isFault(err) {
		return err
	}

	if errors.Is(err, context.Canceled) {
		return err
	}

	if isTimeout(err) {
		return &TimeoutError{Err: err}
	}

	var sc statusCoder
	if errors.As(err, &sc) {
		switch sc.StatusCode() {
		case http.StatusTooManyRequests:
			return &RateLimitError{Err: err}
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return &UnavailableError{Err: err}
		}
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return &UnavailableError{Err: err}
	}

	if isTransport(err) {
		return &TransportError{Err: err}
	}

	return err
}

// WrapIO marks err as an IOError (filesystem boundary), keeping the classified cause.
// Already classified IOError and nil are returned as is.
func WrapIO(err error) error {
	if err == nil {
		return nil
	}

	var ioErr *IOError
	if errors.As(err, &ioErr) {
		return err
	}

	return &IOError{Err: Classify(err)}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isTransport(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr)
}

// isFault reports whether err chain already contains a shared fault.
func isFault(err error) bool {
	var (
		transport   *TransportError
		timeout     *TimeoutError
		ioErr       *IOError
		unavailable *UnavailableError
		rateLimit   *RateLimitError
		docker      *DockerAPIError
	)

	return errors.As(err, &transport) ||
		errors.As(err, &timeout) ||
		errors.As(err, &ioErr) ||
		errors.As(err, &unavailable) ||
		errors.As(err, &rateLimit) ||
		errors.As(err, &docker)
}
