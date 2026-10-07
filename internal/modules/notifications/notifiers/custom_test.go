package notifiers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/httpx"
)

func TestCustomWebhookNotifierNotify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{
			name:       "successful response with body",
			statusCode: http.StatusOK,
		},
		{
			name:       "successful empty response",
			statusCode: http.StatusNoContent,
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

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				assert.Equal(t, http.MethodPut, req.Method)
				assert.Equal(t, "header-value", req.Header.Get("X-Test-Header"))
				w.WriteHeader(tt.statusCode)
				if tt.statusCode != http.StatusNoContent {
					_, _ = w.Write([]byte("response body"))
				}
			}))
			t.Cleanup(server.Close)

			notifier := newCustomWebhookNotifier(
				"test",
				server.URL,
				http.MethodPut,
				map[string]string{"X-Test-Header": "header-value"},
			)

			err := notifier.Notify(context.Background(), Message{})
			if !tt.wantError {
				require.NoError(t, err)
				return
			}

			var codeErr *httpx.CodeError
			require.Error(t, err)
			assert.True(t, errors.As(err, &codeErr))
			assert.Equal(t, tt.statusCode, codeErr.Code)
		})
	}
}
