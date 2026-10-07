package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

// Client sends HTTP requests and maps failures to package-specific error types.
type Client struct {
	httpClient *http.Client
}

// NewClient creates an HTTP client wrapper.
func NewClient(httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient}
}

// SendJSONRequest sends req and unmarshals a successful JSON response into out.
func (c *Client) SendJSONRequest(req *http.Request, out any) error {
	return c.SendRequest(req, json.Unmarshal, out)
}

// SendRequest sends req and unmarshals a successful response into out.
func (c *Client) SendRequest(req *http.Request, unmarshaler Unmarshaler, out any) error {
	resp, err := c.httpClient.Do(req) //nolint:gosec // Callers construct requests and enforce destination policies.
	if err != nil {
		return MatchError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return &CodeError{
			Code: resp.StatusCode,
			Err:  fmt.Errorf("unexpected HTTP status: %s", resp.Status),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &TransportError{Err: fmt.Errorf("read HTTP response body: %w", err)}
	}

	if err = unmarshaler(body, out); err != nil {
		return fmt.Errorf("unmarshal HTTP response: %w", err)
	}

	return nil
}

func MatchError(err error) error {
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

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &InvalidURLError{Err: err}
	}

	return &TransportError{Err: err}
}
