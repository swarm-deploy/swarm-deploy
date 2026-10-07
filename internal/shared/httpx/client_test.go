package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientSendRequestResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{
			name:       "successful response",
			statusCode: http.StatusOK,
		},
		{
			name:       "redirect response",
			statusCode: http.StatusTemporaryRedirect,
		},
		{
			name:       "client error response",
			statusCode: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "server error response",
			statusCode: http.StatusInternalServerError,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = io.WriteString(w, "response body")
			}))
			t.Cleanup(server.Close)

			httpClient := server.Client()
			httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			require.NoError(t, err)

			resp, err := NewClient(httpClient).SendRequest(req)
			require.NotNil(t, resp)
			t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
			assert.Equal(t, tt.statusCode, resp.StatusCode)

			if !tt.wantError {
				require.NoError(t, err)
				return
			}

			var codeErr *CodeError
			require.ErrorAs(t, err, &codeErr)
			assert.Equal(t, tt.statusCode, codeErr.Code)
			assert.Error(t, codeErr.Err)
		})
	}
}

func TestClientSendRequestFailure(t *testing.T) {
	t.Parallel()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	timedOutCtx, cancelTimeout := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancelTimeout)

	closedServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedServerURL := closedServer.URL
	closedServer.Close()

	tests := []struct {
		name       string
		buildReq   func(t *testing.T) *http.Request
		assertType func(t *testing.T, err error)
	}{
		{
			name: "nil request",
			buildReq: func(*testing.T) *http.Request {
				return nil
			},
			assertType: assertErrorType[*InvalidURLError],
		},
		{
			name: "missing URL scheme",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, closedServerURL, nil)
				require.NoError(t, err)
				req.URL.Scheme = ""
				return req
			},
			assertType: assertErrorType[*InvalidURLError],
		},
		{
			name: "canceled request",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(canceledCtx, http.MethodGet, closedServerURL, nil)
				require.NoError(t, err)
				return req
			},
			assertType: assertErrorType[*RequestCanceledError],
		},
		{
			name: "timed out request",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(timedOutCtx, http.MethodGet, closedServerURL, nil)
				require.NoError(t, err)
				return req
			},
			assertType: assertErrorType[*TimeoutError],
		},
		{
			name: "transport failure",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, closedServerURL, nil)
				require.NoError(t, err)
				return req
			},
			assertType: assertErrorType[*TransportError],
		},
	}

	client := NewClient(http.DefaultClient)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := client.SendRequest(tt.buildReq(t))
			assert.Nil(t, resp)
			require.Error(t, err)
			tt.assertType(t, err)
		})
	}
}

func TestErrorTemporary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       Error
		temporary bool
	}{
		{
			name: "client status error",
			err:  &CodeError{Code: http.StatusBadRequest, Err: errors.New("bad request")},
		},
		{
			name:      "server status error",
			err:       &CodeError{Code: http.StatusServiceUnavailable, Err: errors.New("unavailable")},
			temporary: true,
		},
		{
			name:      "transport error",
			err:       &TransportError{Err: errors.New("connection failed")},
			temporary: true,
		},
		{
			name: "permanent DNS error",
			err: &DNSError{Err: &net.DNSError{
				Err:  "host not found",
				Name: "example.com",
			}},
		},
		{
			name: "temporary DNS error",
			err: &DNSError{Err: &net.DNSError{
				Err:         "server misbehaving",
				Name:        "example.com",
				IsTemporary: true,
			}},
			temporary: true,
		},
		{
			name: "invalid URL error",
			err:  &InvalidURLError{Err: errors.New("invalid URL")},
		},
		{
			name:      "timeout error",
			err:       &TimeoutError{Err: context.DeadlineExceeded},
			temporary: true,
		},
		{
			name: "request canceled error",
			err:  &RequestCanceledError{Err: context.Canceled},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.temporary, tt.err.Temporary())
		})
	}
}

func TestClassifyRequestErrorDNS(t *testing.T) {
	t.Parallel()

	underlyingErr := &net.DNSError{
		Err:         "server misbehaving",
		Name:        "example.com",
		IsTemporary: true,
	}
	requestErr := &url.Error{
		Op:  http.MethodGet,
		URL: "https://example.com",
		Err: underlyingErr,
	}

	err := classifyRequestError(requestErr)

	var dnsErr *DNSError
	require.ErrorAs(t, err, &dnsErr)
	assert.ErrorIs(t, err, underlyingErr)
	assert.True(t, dnsErr.Temporary())
}

func assertErrorType[T error](t *testing.T, err error) {
	t.Helper()

	var target T
	require.True(t, errors.As(err, &target))
	assert.Error(t, errors.Unwrap(err))
}
