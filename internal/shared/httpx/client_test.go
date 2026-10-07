package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientSendJSONRequestResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantError  bool
		wantValue  string
	}{
		{
			name:       "successful response",
			statusCode: http.StatusOK,
			wantValue:  "ok",
		},
		{
			name:       "redirect response",
			statusCode: http.StatusTemporaryRedirect,
			wantValue:  "redirect",
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
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_ = json.NewEncoder(w).Encode(map[string]string{"value": tt.wantValue})
			}))
			t.Cleanup(server.Close)

			httpClient := server.Client()
			httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			require.NoError(t, err)

			out := struct {
				Value string `json:"value"`
			}{}
			err = NewClient(httpClient).SendJSONRequest(req, &out)

			if !tt.wantError {
				require.NoError(t, err)
				assert.Equal(t, tt.wantValue, out.Value)
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

	const requestURL = "http://example.invalid"

	tests := []struct {
		name       string
		buildReq   func(t *testing.T) *http.Request
		assertType func(t *testing.T, err error)
	}{
		{
			name: "nil request URL",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, requestURL, nil)
				require.NoError(t, err)
				req.URL = nil
				return req
			},
			assertType: assertErrorType[*InvalidURLError],
		},
		{
			name: "missing URL scheme",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, requestURL, nil)
				require.NoError(t, err)
				req.URL.Scheme = ""
				return req
			},
			assertType: assertErrorType[*InvalidURLError],
		},
		{
			name: "canceled request",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(canceledCtx, http.MethodGet, requestURL, nil)
				require.NoError(t, err)
				return req
			},
			assertType: assertErrorType[*RequestCanceledError],
		},
		{
			name: "timed out request",
			buildReq: func(t *testing.T) *http.Request {
				req, err := http.NewRequestWithContext(timedOutCtx, http.MethodGet, requestURL, nil)
				require.NoError(t, err)
				return req
			},
			assertType: assertErrorType[*TimeoutError],
		},
	}

	client := NewClient(http.DefaultClient)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := client.SendRequest(tt.buildReq(t), json.Unmarshal, &struct{}{})
			require.Error(t, err)
			tt.assertType(t, err)
		})
	}
}

func TestClassifyRequestErrorTransport(t *testing.T) {
	t.Parallel()

	err := MatchError(errors.New("transport failure"))

	assertErrorType[*TransportError](t, err)
}

func TestClientSendRequestUnmarshalFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		unmarshaler Unmarshaler
		assertError func(t *testing.T, err error)
	}{
		{
			name:        "invalid response",
			unmarshaler: json.Unmarshal,
			assertError: func(t *testing.T, err error) {
				var syntaxErr *json.SyntaxError
				require.ErrorAs(t, err, &syntaxErr)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("not-json"))
			}))
			t.Cleanup(server.Close)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			require.NoError(t, err)

			err = NewClient(server.Client()).SendRequest(req, tt.unmarshaler, &struct{}{})
			require.Error(t, err)
			tt.assertError(t, err)
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

	err := MatchError(requestErr)

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
