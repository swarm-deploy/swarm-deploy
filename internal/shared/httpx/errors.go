package httpx

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

// Error is an error returned by Client.
type Error interface {
	error

	// Temporary reports whether retrying the request may succeed.
	Temporary() bool

	// isHttpErr marks errors produced by Client.
	isHttpErr()
}

// CodeError reports an unsuccessful HTTP response status.
type CodeError struct {
	// Code is the HTTP response status code.
	Code int
	// Err describes the response failure.
	Err error
}

// Error returns the HTTP status failure message.
func (e *CodeError) Error() string {
	return fmt.Sprintf("HTTP request failed with status code %d: %v", e.Code, e.Err)
}

// Unwrap returns the underlying response failure.
func (e *CodeError) Unwrap() error {
	return e.Err
}

// Temporary reports whether the response contains a server-side error.
func (e *CodeError) Temporary() bool {
	return e.Code >= http.StatusInternalServerError
}

func (e *CodeError) isHttpErr() {}

// TransportError reports a failure while sending a request or receiving its response.
type TransportError struct {
	// Err is the underlying transport failure.
	Err error
}

// Error returns the transport failure message.
func (e *TransportError) Error() string {
	return fmt.Sprintf("HTTP transport failed: %v", e.Err)
}

// Unwrap returns the underlying transport failure.
func (e *TransportError) Unwrap() error {
	return e.Err
}

// Temporary reports that retrying a transport failure may succeed.
func (e *TransportError) Temporary() bool {
	return true
}

func (e *TransportError) isHttpErr() {}

// DNSError reports a failure while resolving the request host.
type DNSError struct {
	// Err is the underlying DNS failure.
	Err error
}

// Error returns the DNS failure message.
func (e *DNSError) Error() string {
	return fmt.Sprintf("HTTP DNS lookup failed: %v", e.Err)
}

// Unwrap returns the underlying DNS failure.
func (e *DNSError) Unwrap() error {
	return e.Err
}

// Temporary reports whether the underlying DNS failure is temporary.
func (e *DNSError) Temporary() bool {
	var dnsErr *net.DNSError
	return errors.As(e.Err, &dnsErr) && dnsErr.Temporary()
}

func (e *DNSError) isHttpErr() {}

// InvalidURLError reports a request with an invalid HTTP URL.
type InvalidURLError struct {
	// Err describes why the URL is invalid.
	Err error
}

// Error returns the invalid URL failure message.
func (e *InvalidURLError) Error() string {
	return fmt.Sprintf("invalid HTTP request URL: %v", e.Err)
}

// Unwrap returns the underlying URL failure.
func (e *InvalidURLError) Unwrap() error {
	return e.Err
}

// Temporary reports that an invalid URL cannot be fixed by retrying.
func (e *InvalidURLError) Temporary() bool {
	return false
}

func (e *InvalidURLError) isHttpErr() {}

// TimeoutError reports that an HTTP request timed out.
type TimeoutError struct {
	// Err is the underlying timeout failure.
	Err error
}

// Error returns the timeout failure message.
func (e *TimeoutError) Error() string {
	return fmt.Sprintf("HTTP request timed out: %v", e.Err)
}

// Unwrap returns the underlying timeout failure.
func (e *TimeoutError) Unwrap() error {
	return e.Err
}

// Temporary reports that retrying a timed-out request may succeed.
func (e *TimeoutError) Temporary() bool {
	return true
}

func (e *TimeoutError) isHttpErr() {}

// RequestCanceledError reports that an HTTP request was canceled.
type RequestCanceledError struct {
	// Err is the underlying cancellation failure.
	Err error
}

// Error returns the cancellation failure message.
func (e *RequestCanceledError) Error() string {
	return fmt.Sprintf("HTTP request canceled: %v", e.Err)
}

// Unwrap returns the underlying cancellation failure.
func (e *RequestCanceledError) Unwrap() error {
	return e.Err
}

// Temporary reports that an explicitly canceled request should not be retried.
func (e *RequestCanceledError) Temporary() bool {
	return false
}

func (e *RequestCanceledError) isHttpErr() {}

var (
	_ Error = (*CodeError)(nil)
	_ Error = (*TransportError)(nil)
	_ Error = (*DNSError)(nil)
	_ Error = (*InvalidURLError)(nil)
	_ Error = (*TimeoutError)(nil)
	_ Error = (*RequestCanceledError)(nil)
)
