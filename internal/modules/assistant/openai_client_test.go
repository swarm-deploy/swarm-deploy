package assistant

import (
	"net/http"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

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
