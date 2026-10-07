package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// SendJSONRequest sends req and unmarshals a successful JSON response into out.
func (c *Client) SendJSONRequest(req *http.Request, out any) error {
	return c.SendRequest(req, json.Unmarshal, out)
}

// SendRequest sends req and unmarshals a successful response into out.
func (c *Client) SendRequest(req *http.Request, unmarshaler Unmarshaler, out any) error {
	if err := validateRequest(req); err != nil {
		return &InvalidURLError{Err: err}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return classifyRequestError(err)
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
