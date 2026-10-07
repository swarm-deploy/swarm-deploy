package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// Client sends HTTP requests and maps failures to package-specific error types.
type Client struct {
	httpClient *http.Client
}

// NewClient creates an HTTP client wrapper.
func NewClient(httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient}
}

// SendRequest sends req and classifies HTTP and transport failures.
//
// A response with a status code greater than or equal to 400 is returned
// together with a CodeError. The caller remains responsible for closing the
// response body whenever the returned response is non-nil.
func (c *Client) SendRequest(req *http.Request) (*http.Response, error) {
	if err := validateRequest(req); err != nil {
		return nil, &InvalidURLError{Err: err}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return resp, classifyRequestError(err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return resp, &CodeError{
			Code: resp.StatusCode,
			Err:  fmt.Errorf("unexpected HTTP status: %s", resp.Status),
		}
	}

	return resp, nil
}

func validateRequest(req *http.Request) error {
	if req == nil {
		return errors.New("request is nil")
	}
	if req.URL == nil {
		return errors.New("request URL is nil")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", req.URL.Scheme)
	}
	if req.URL.Host == "" {
		return errors.New("request URL has no host")
	}

	return nil
}

func classifyRequestError(err error) error {
	if errors.Is(err, context.Canceled) {
		return &RequestCanceledError{Err: err}
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &DNSError{Err: err}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{Err: err}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &TimeoutError{Err: err}
	}

	return &TransportError{Err: err}
}
