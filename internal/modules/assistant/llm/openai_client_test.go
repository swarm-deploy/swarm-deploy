package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func TestEmbedOmitsProviderErrorContentAndPreservesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/embeddings", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, err := fmt.Fprint(w, `{"error":{"message":"private-query-and-service-metadata","type":"invalid_request_error","code":"private-provider-code"},"usage":{"prompt_tokens":7,"total_tokens":7}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "synthetic-api-key", "", "")
	vectors, usage, err := client.Embed(context.Background(), "embedding-model", []string{"private-query"})
	require.EqualError(t, err, "embeddings request failed with HTTP status 400")
	require.Nil(t, vectors)
	require.EqualValues(t, 7, usage.InputTokens)
	require.EqualValues(t, 7, usage.TotalTokens)
}

func TestSafeProviderErrorOmitsTransportURLAndPreservesCancellation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
		is   error
	}{
		{
			name: "transport URL",
			err: &url.Error{Op: "Post", URL: "https://provider.invalid/secret-path?token=private-token",
				Err: fmt.Errorf("private transport diagnostics")},
			want: "embeddings request failed",
		},
		{
			name: "canceled", err: fmt.Errorf("private wrapper: %w", context.Canceled),
			want: "embeddings request failed: context canceled", is: context.Canceled,
		},
		{
			name: "deadline", err: fmt.Errorf("private wrapper: %w", context.DeadlineExceeded),
			want: "embeddings request failed: context deadline exceeded", is: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := safeProviderError("embeddings", tt.err)
			require.EqualError(t, err, tt.want)
			if tt.is != nil {
				require.ErrorIs(t, err, tt.is)
			}
		})
	}
}

func TestSafeChatCompletionError(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "missing model",
			code: "model_not_found",
			want: "chat completion request failed with HTTP status 400 (model_not_found)",
		},
		{
			name: "unsupported parameter",
			code: "unsupported_parameter",
			want: "chat completion request failed with HTTP status 400 (unsupported_parameter)",
		},
		{
			name: "unknown provider code",
			code: "provider-echoed-sensitive-value",
			want: "chat completion request failed with HTTP status 400",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &openai.Error{
				StatusCode: http.StatusBadRequest,
				Code:       tt.code,
				Message:    "provider error message may contain request data",
			}
			require.EqualError(t, safeChatCompletionError(err), tt.want)
		})
	}
}
